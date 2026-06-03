package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	recordTypeHandshake      = 22
	handshakeTypeClientHello = 1
	extensionTypeSNI         = 0
	sniNameTypeHost          = 0
	tcpKeepAlive             = 30 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultBufSize           = 65535
)

type Proxy struct {
	addr        string
	dialer      net.Dialer
	resolver    *net.Resolver
	readTimeout time.Duration
	idleTimeout time.Duration
}

func NewProxy(addr string) *Proxy {
	return &Proxy{
		addr: addr,
		dialer: net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: tcpKeepAlive,
		},
		resolver:    net.DefaultResolver,
		readTimeout: defaultReadTimeout,
		idleTimeout: defaultIdleTimeout,
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	addr := env("SNI_PROXY_ADDR", ":443")
	proxy := NewProxy(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := proxy.ListenAndServe(ctx); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Error("sniproxy server error", "error", err)
		}
	}()

	slog.Info("SNI Proxy started", "addr", addr)

	<-sigCh
	slog.Info("shutting down SNI Proxy...")
	cancel()
	wg.Wait()
	slog.Info("SNI Proxy stopped")
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func (p *Proxy) ListenAndServe(ctx context.Context) error {
	lc := net.ListenConfig{
		KeepAlive: tcpKeepAlive,
	}
	ln, err := lc.Listen(ctx, "tcp", p.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", p.addr, err)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			slog.Error("accept error", "error", err)
			continue
		}
		go p.handleConn(conn)
	}
}

func (p *Proxy) handleConn(client net.Conn) {
	defer client.Close()

	if tcp, ok := client.(*net.TCPConn); ok {
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(tcpKeepAlive)
	}
	client.SetReadDeadline(time.Now().Add(p.readTimeout))

	sni, clientHello, err := p.parseClientHello(client)
	if err != nil {
		slog.Warn("parse client hello failed",
			"remote", client.RemoteAddr(),
			"error", err,
		)
		return
	}

	slog.Info("sni detected",
		"sni", sni,
		"remote", client.RemoteAddr(),
	)

	client.SetReadDeadline(time.Time{})

	ips, err := p.resolver.LookupHost(context.Background(), sni)
	if err != nil || len(ips) == 0 {
		slog.Warn("dns resolution failed",
			"sni", sni,
			"error", err,
		)
		return
	}

	target := net.JoinHostPort(ips[0], "443")
	upstream, err := p.dialer.DialContext(context.Background(), "tcp", target)
	if err != nil {
		slog.Warn("upstream dial failed",
			"sni", sni,
			"target", target,
			"error", err,
		)
		return
	}
	defer upstream.Close()

	if _, err := upstream.Write(clientHello); err != nil {
		slog.Warn("replay client hello failed",
			"sni", sni,
			"error", err,
		)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		transfer(upstream, client, p.idleTimeout)
	}()
	go func() {
		defer wg.Done()
		transfer(client, upstream, p.idleTimeout)
	}()

	wg.Wait()
}

func transfer(dst, src net.Conn, idleTimeout time.Duration) {
	buf := make([]byte, 32768)
	for {
		src.SetReadDeadline(time.Now().Add(idleTimeout))
		n, err := src.Read(buf)
		if n > 0 {
			dst.SetWriteDeadline(time.Now().Add(idleTimeout))
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !isTimeoutErr(err) {
				return
			}
			return
		}
	}
}

func (p *Proxy) parseClientHello(conn net.Conn) (sni string, raw []byte, err error) {
	record, err := readRecord(conn)
	if err != nil {
		return "", nil, fmt.Errorf("read record: %w", err)
	}

	if record.ContentType != recordTypeHandshake {
		return "", nil, fmt.Errorf("expected handshake record, got %d", record.ContentType)
	}

	if len(record.Payload) < 1 {
		return "", nil, fmt.Errorf("empty handshake payload")
	}

	if record.Payload[0] != handshakeTypeClientHello {
		return "", nil, fmt.Errorf("expected client hello, got %d", record.Payload[0])
	}

	helloData := record.Payload
	if len(helloData) < 4 {
		return "", nil, fmt.Errorf("truncated handshake header")
	}

	handshakeLen := uint24(helloData[1:4])
	helloBody := helloData[4:]
	if len(helloBody) < handshakeLen {
		return "", nil, fmt.Errorf("truncated handshake body")
	}
	helloBody = helloBody[:handshakeLen]

	sni, err = extractSNI(helloBody)
	if err != nil {
		return "", nil, fmt.Errorf("extract sni: %w", err)
	}

	raw = record.Raw
	return sni, raw, nil
}

type tlsRecord struct {
	ContentType uint8
	Version     uint16
	Length      uint16
	Payload     []byte
	Raw         []byte
}

func readRecord(conn io.Reader) (*tlsRecord, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	r := &tlsRecord{
		ContentType: header[0],
		Version:     binary.BigEndian.Uint16(header[1:3]),
		Length:      binary.BigEndian.Uint16(header[3:5]),
	}

	payload := make([]byte, r.Length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	r.Payload = payload
	r.Raw = make([]byte, 0, 5+len(payload))
	r.Raw = append(r.Raw, header...)
	r.Raw = append(r.Raw, payload...)

	return r, nil
}

func extractSNI(clientHello []byte) (string, error) {
	if len(clientHello) < 2 {
		return "", fmt.Errorf("client hello too short for version")
	}

	pos := 2

	if len(clientHello) < pos+32 {
		return "", fmt.Errorf("client hello too short for random")
	}
	pos += 32

	if len(clientHello) < pos+1 {
		return "", fmt.Errorf("client hello too short for session id length")
	}
	sessionIDLen := int(clientHello[pos])
	pos++
	if len(clientHello) < pos+sessionIDLen {
		return "", fmt.Errorf("client hello truncated in session id")
	}
	pos += sessionIDLen

	if len(clientHello) < pos+2 {
		return "", fmt.Errorf("client hello too short for cipher suites length")
	}
	cipherSuiteLen := int(binary.BigEndian.Uint16(clientHello[pos:]))
	pos += 2
	if len(clientHello) < pos+cipherSuiteLen {
		return "", fmt.Errorf("client hello truncated in cipher suites")
	}
	pos += cipherSuiteLen

	if len(clientHello) < pos+1 {
		return "", fmt.Errorf("client hello too short for compression methods length")
	}
	compLen := int(clientHello[pos])
	pos++
	if len(clientHello) < pos+compLen {
		return "", fmt.Errorf("client hello truncated in compression methods")
	}
	pos += compLen

	if len(clientHello) < pos+2 {
		return "", fmt.Errorf("client hello too short for extensions length")
	}
	extLen := int(binary.BigEndian.Uint16(clientHello[pos:]))
	pos += 2
	if extLen == 0 {
		return "", fmt.Errorf("no extensions")
	}

	end := pos + extLen
	if len(clientHello) < end {
		return "", fmt.Errorf("client hello truncated in extensions")
	}

	for pos+4 <= end {
		extType := binary.BigEndian.Uint16(clientHello[pos:])
		extDataLen := int(binary.BigEndian.Uint16(clientHello[pos+2:]))
		pos += 4
		if pos+extDataLen > end {
			return "", fmt.Errorf("extension data truncated")
		}

		if extType == extensionTypeSNI {
			return parseSNI(clientHello[pos : pos+extDataLen])
		}

		pos += extDataLen
	}

	return "", fmt.Errorf("sni extension not found")
}

func uint24(b []byte) int {
	if len(b) < 3 {
		return 0
	}
	return int(b[0])<<16 | int(b[1])<<8 | int(b[2])
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func parseSNI(data []byte) (string, error) {
	if len(data) < 2 {
		return "", fmt.Errorf("sni list too short")
	}

	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	if listLen == 0 {
		return "", fmt.Errorf("empty sni list")
	}

	pos := 2
	end := pos + listLen
	if len(data) < end {
		return "", fmt.Errorf("sni list truncated")
	}

	if pos+3 > end {
		return "", fmt.Errorf("sni entry truncated")
	}

	nameType := data[pos]
	if nameType != sniNameTypeHost {
		return "", fmt.Errorf("unsupported sni name type: %d", nameType)
	}
	pos++

	nameLen := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if pos+nameLen > end {
		return "", fmt.Errorf("sni name truncated")
	}

	return string(data[pos : pos+nameLen]), nil
}

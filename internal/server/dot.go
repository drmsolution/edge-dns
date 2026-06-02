package server

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/miekg/dns"
	"github.com/user/edge-dns/internal/cert"
	"github.com/user/edge-dns/internal/handler"
)

func RunDoT(ctx context.Context, addr string) error {
	tlsCert, err := cert.GenerateSelfSigned()
	if err != nil {
		return err
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			return nil, nil
		},
	}

	listener, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		return err
	}

	slog.Info("starting DoT server", "addr", addr, "proto", "DoT")

	var wg sync.WaitGroup

	go func() {
		<-ctx.Done()
		slog.Info("shutting down DoT server")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				wg.Wait()
				return ctx.Err()
			default:
				slog.Error("DoT accept", "error", err)
				continue
			}
		}

		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			handleDoTConnection(c)
		}(conn)
	}
}

func handleDoTConnection(conn net.Conn) {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		slog.Error("not a TLS connection")
		conn.Close()
		return
	}

	if err := tlsConn.Handshake(); err != nil {
		slog.Warn("TLS handshake failed", "remote", conn.RemoteAddr(), "error", err)
		conn.Close()
		return
	}

	state := tlsConn.ConnectionState()
	serverName := state.ServerName

	userID, err := handler.ExtractUserFromSNI(serverName)
	if err != nil {
		slog.Warn("extract user from SNI", "sni", serverName, "remote", conn.RemoteAddr(), "error", err)
		userID = "unknown"
	}

	slog.Info("DoT connection established",
		"user_id", userID,
		"sni", serverName,
		"remote", conn.RemoteAddr(),
		"tls_version", tlsVersionString(state.Version),
	)

	defer conn.Close()

	for {
		msg, err := readMsgFromConn(tlsConn)
		if err != nil {
			if err.Error() != "EOF" && err.Error() != "unexpected EOF" {
				slog.Warn("read DNS from DoT",
					"user_id", userID,
					"error", err,
				)
			}
			return
		}

		dotWriter := &dotResponseWriter{
			conn:       tlsConn,
			remoteAddr: conn.RemoteAddr().String(),
		}

		handler.HandleDNSQuery(userID, dotWriter, msg)
	}
}

func readMsgFromConn(conn net.Conn) (*dns.Msg, error) {
	buf := make([]byte, 2)
	if _, err := conn.Read(buf); err != nil {
		return nil, err
	}

	length := int(buf[0])<<8 | int(buf[1])
	if length > 65535 {
		return nil, dns.ErrBuf
	}

	data := make([]byte, length)
	_, err := readFull(conn, data)
	if err != nil {
		return nil, err
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(data); err != nil {
		return nil, err
	}

	return msg, nil
}

type dotResponseWriter struct {
	conn       net.Conn
	remoteAddr string
}

func (w *dotResponseWriter) WriteMsg(msg *dns.Msg) error {
	return writeMsgToConn(w.conn, msg)
}

func (w *dotResponseWriter) Write(p []byte) (int, error) {
	return w.conn.Write(p)
}

func (w *dotResponseWriter) RemoteAddr() net.Addr {
	return dotAddr(w.remoteAddr)
}

func (w *dotResponseWriter) LocalAddr() net.Addr {
	return dotAddr("")
}

func (w *dotResponseWriter) TsigStatus() error {
	return nil
}

func (w *dotResponseWriter) TsigTimersOnly(b bool) {}

func (w *dotResponseWriter) Close() error {
	return w.conn.Close()
}

func (w *dotResponseWriter) Hijack() {}

func writeMsgToConn(conn net.Conn, msg *dns.Msg) error {
	packed, err := msg.Pack()
	if err != nil {
		return err
	}

	length := len(packed)
	header := []byte{byte(length >> 8), byte(length & 0xff)}

	if _, err := conn.Write(header); err != nil {
		return err
	}
	if _, err := conn.Write(packed); err != nil {
		return err
	}
	return nil
}

type dotAddr string

func (a dotAddr) Network() string { return "dot" }
func (a dotAddr) String() string  { return string(a) }

func readFull(r io.Reader, buf []byte) (int, error) {
	return io.ReadFull(r, buf)
}

func tlsVersionString(v uint16) string {
	switch v {
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	default:
		return "unknown"
	}
}

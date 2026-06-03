package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/miekg/dns"
	"github.com/user/edge-dns/internal/cert"
	"github.com/user/edge-dns/internal/handler"
)

const dnsMimeType = "application/dns-message"

func RunDoH(ctx context.Context, addr string) error {
	tlsCert, err := cert.LoadFromEnv()
	if err != nil {
		return err
	}

	tlsCertFile := os.Getenv("TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("TLS_KEY_FILE")
	if tlsCertFile != "" && tlsKeyFile != "" {
		slog.Info("DoH using user-supplied TLS certificate", "cert_file", tlsCertFile)
	} else {
		slog.Info("DoH using self-signed TLS certificate (set TLS_CERT_FILE/TLS_KEY_FILE for production)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDoH)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down DoH server")
		srv.Shutdown(ctx)
	}()

	slog.Info("starting DoH server", "addr", addr, "proto", "DoH")
	if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func handleDoH(w http.ResponseWriter, r *http.Request) {
	userID, err := handler.ExtractUserFromDoHPath(r.URL.Path)
	if err != nil {
		slog.Warn("invalid DoH path", "path", r.URL.Path, "error", err)
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	var msgBytes []byte
	switch r.Method {
	case http.MethodGet:
		dnsParam := r.URL.Query().Get("dns")
		if dnsParam == "" {
			http.Error(w, "missing dns parameter", http.StatusBadRequest)
			return
		}
		msgBytes, err = base64.RawURLEncoding.DecodeString(dnsParam)
		if err != nil {
			slog.Warn("invalid base64", "error", err)
			http.Error(w, "invalid base64", http.StatusBadRequest)
			return
		}
	case http.MethodPost:
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, dnsMimeType) {
			slog.Warn("invalid content type", "content_type", ct)
			http.Error(w, "expected application/dns-message", http.StatusUnsupportedMediaType)
			return
		}
		msgBytes, err = io.ReadAll(io.LimitReader(r.Body, 65535))
		if err != nil {
			slog.Warn("read body", "error", err)
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(msgBytes); err != nil {
		slog.Warn("unpack DNS message", "error", err)
		http.Error(w, "invalid DNS message", http.StatusBadRequest)
		return
	}

	respWriter := &dohResponseWriter{
		httpWriter: w,
		remoteAddr: r.RemoteAddr,
	}

	handler.HandleDNSQuery(userID, respWriter, msg)
}

type dohResponseWriter struct {
	httpWriter http.ResponseWriter
	remoteAddr string
}

func (w *dohResponseWriter) WriteMsg(msg *dns.Msg) error {
	packed, err := msg.Pack()
	if err != nil {
		return err
	}
	w.httpWriter.Header().Set("Content-Type", dnsMimeType)
	w.httpWriter.WriteHeader(http.StatusOK)
	_, err = w.httpWriter.Write(packed)
	return err
}

func (w *dohResponseWriter) Write(p []byte) (int, error) {
	return w.httpWriter.Write(p)
}

func (w *dohResponseWriter) RemoteAddr() net.Addr {
	return dohAddr(w.remoteAddr)
}

func (w *dohResponseWriter) LocalAddr() net.Addr {
	return dohAddr("")
}

func (w *dohResponseWriter) TsigStatus() error {
	return nil
}

func (w *dohResponseWriter) TsigTimersOnly(b bool) {}

func (w *dohResponseWriter) Close() error {
	return nil
}

func (w *dohResponseWriter) Hijack() {}

type dohAddr string

func (a dohAddr) Network() string { return "doh" }
func (a dohAddr) String() string  { return string(a) }

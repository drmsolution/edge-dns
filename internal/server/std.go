package server

import (
	"context"
	"log/slog"

	"github.com/miekg/dns"
	"github.com/user/edge-dns/internal/handler"
)

const DefaultUserID = "default"

type stdHandler struct {
	userID string
}

func (h *stdHandler) ServeDNS(w dns.ResponseWriter, msg *dns.Msg) {
	handler.HandleDNSQuery(h.userID, w, msg)
}

func RunStd(ctx context.Context, addr string) error {
	h := &stdHandler{userID: DefaultUserID}

	udpSrv := &dns.Server{
		Addr:    addr,
		Net:     "udp",
		Handler: h,
	}
	tcpSrv := &dns.Server{
		Addr:    addr,
		Net:     "tcp",
		Handler: h,
	}

	errCh := make(chan error, 2)

	go func() {
		slog.Info("starting UDP DNS server", "addr", addr, "proto", "UDP")
		if err := udpSrv.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	go func() {
		slog.Info("starting TCP DNS server", "addr", addr, "proto", "TCP")
		if err := tcpSrv.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down UDP/TCP DNS servers")
		udpSrv.ShutdownContext(ctx)
		tcpSrv.ShutdownContext(ctx)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

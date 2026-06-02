package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/miekg/dns"
	"github.com/user/edge-dns/internal/analytics"
	"github.com/user/edge-dns/internal/metrics"
	"github.com/user/edge-dns/internal/ratelimit"
	"github.com/user/edge-dns/internal/rule"
	"github.com/user/edge-dns/internal/upstream"
)

var defaultAggregator *analytics.LogAggregator
var defaultRateLimiter *ratelimit.RateLimiter

func SetAggregator(a *analytics.LogAggregator) {
	defaultAggregator = a
}

func SetRateLimiter(rl *ratelimit.RateLimiter) {
	defaultRateLimiter = rl
}

func New() *Handler {
	return &Handler{}
}

type Handler struct{}

type Result int

const (
	ResultAllow Result = iota
	ResultBlock
)

func (h *Handler) ProcessQuery(userID string, w dns.ResponseWriter, msg *dns.Msg) {
	start := time.Now()

	if defaultRateLimiter != nil {
		allowed, err := defaultRateLimiter.AllowQuery(context.Background(), userID, 100, time.Second)
		if err != nil {
			slog.Error("rate limiter error", "error", err)
		} else if !allowed {
			writeRateLimitResponse(w, msg)
			return
		}
	}

	if len(msg.Question) == 0 {
		slog.Warn("empty question", "user_id", userID, "remote", w.RemoteAddr())
		return
	}

	q := msg.Question[0]
	domain := q.Name
	qtype := dns.TypeToString[q.Qtype]

	slog.Info("query",
		"user_id", userID,
		"domain", domain,
		"qtype", qtype,
		"protocol", getProtocol(w),
		"remote", w.RemoteAddr(),
	)

	status := analytics.StatusAllowed
	result := rule.CheckRule(userID, domain)

	switch result {
	case 1:
		writeBlockResponse(w, msg, q)
		status = analytics.StatusBlocked
	default:
		forwardResponse(w, msg)
	}

	elapsed := time.Since(start)
	protocol := getProtocol(w)

	metrics.DNSQueriesTotal.WithLabelValues(protocol, string(status), qtype).Inc()
	metrics.DNSQueryDurationSeconds.WithLabelValues(protocol, string(status)).Observe(elapsed.Seconds())

	h.submitLog(userID, w, domain, qtype, status, start)
}

func (h *Handler) submitLog(userID string, w dns.ResponseWriter, domain, qtype string, status analytics.LogStatus, start time.Time) {
	if defaultAggregator == nil {
		return
	}

	elapsed := time.Since(start)
	clientIP := extractClientIP(w)

	defaultAggregator.SubmitLog(analytics.DNSLog{
		Timestamp:    start,
		UserID:       userID,
		ClientIP:     clientIP,
		Domain:       domain,
		QueryType:    qtype,
		Status:       status,
		ResponseTime: elapsed,
	})
}

func extractClientIP(w dns.ResponseWriter) string {
	addr := w.RemoteAddr().String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func getProtocol(w dns.ResponseWriter) string {
	addr := w.RemoteAddr().Network()
	switch addr {
	case "udp":
		return "UDP"
	case "tcp":
		remote := w.RemoteAddr().String()
		host, _, _ := net.SplitHostPort(remote)
		if host != "" {
			return "DoT"
		}
		return "TCP"
	default:
		return addr
	}
}

func writeRateLimitResponse(w dns.ResponseWriter, msg *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(msg)
	resp.Rcode = dns.RcodeRefused
	if err := w.WriteMsg(resp); err != nil {
		slog.Error("write rate limit response", "error", err)
	}
}

func writeBlockResponse(w dns.ResponseWriter, msg *dns.Msg, q dns.Question) {
	resp := new(dns.Msg)
	resp.SetReply(msg)
	resp.Rcode = dns.RcodeNameError

	rr := &dns.A{
		Hdr: dns.RR_Header{
			Name:   q.Name,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    60,
		},
		A: net.ParseIP("0.0.0.0"),
	}
	resp.Answer = append(resp.Answer, rr)

	if q.Qtype == dns.TypeAAAA {
		resp.Answer = nil
		aaaa := &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    60,
			},
			AAAA: net.ParseIP("::"),
		}
		resp.Answer = append(resp.Answer, aaaa)
	}

	if err := w.WriteMsg(resp); err != nil {
		slog.Error("write block response", "error", err)
	}
}

func forwardResponse(w dns.ResponseWriter, msg *dns.Msg) {
	resp, err := upstream.Resolve(msg)
	if err != nil {
		slog.Error("upstream resolve", "error", err)
		resp = new(dns.Msg)
		resp.SetReply(msg)
		resp.Rcode = dns.RcodeServerFailure

		if writeErr := w.WriteMsg(resp); writeErr != nil {
			slog.Error("write servfail response", "error", writeErr)
		}
		return
	}

	if err := w.WriteMsg(resp); err != nil {
		slog.Error("write upstream response", "error", err, "remote", w.RemoteAddr())
	}
}

func HandleDNSQuery(userID string, w dns.ResponseWriter, msg *dns.Msg) {
	h := &Handler{}
	h.ProcessQuery(userID, w, msg)
}

func ExtractUserFromSNI(serverName string) (string, error) {
	if serverName == "" {
		return "", fmt.Errorf("empty SNI")
	}

	idx := dotIndex(serverName)
	if idx < 0 {
		return "", fmt.Errorf("no subdomain in SNI: %s", serverName)
	}

	userID := serverName[:idx]
	if userID == "" {
		return "", fmt.Errorf("empty user_id in SNI: %s", serverName)
	}

	return userID, nil
}

func dotIndex(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func ExtractUserFromDoHPath(path string) (string, error) {
	if len(path) == 0 || path[0] != '/' {
		return "", fmt.Errorf("invalid path: %s", path)
	}

	path = path[1:]

	slashIdx := -1
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			slashIdx = i
			break
		}
	}

	var userID string
	if slashIdx >= 0 {
		userID = path[:slashIdx]
	} else {
		userID = path
	}

	if userID == "" {
		return "", fmt.Errorf("empty user_id in path: %s", path)
	}

	return userID, nil
}

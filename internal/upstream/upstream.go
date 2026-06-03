package upstream

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/miekg/dns"
)

var (
	UpstreamAddr  = "1.1.1.1:53"
	clientTimeout = 5 * time.Second
	clientUDPSize = uint16(1232)
)

var client = &dns.Client{
	Net:     "udp",
	Timeout: clientTimeout,
	UDPSize: clientUDPSize,
}

func init() {
	if addr := os.Getenv("UPSTREAM_DNS"); addr != "" {
		UpstreamAddr = addr
	}
	if t := os.Getenv("UPSTREAM_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			clientTimeout = d
		}
	}
	if s := os.Getenv("UPSTREAM_UDP_SIZE"); s != "" {
		if n, err := strconv.ParseUint(s, 10, 16); err == nil && n > 0 {
			clientUDPSize = uint16(n)
		}
	}
	client.Timeout = clientTimeout
	client.UDPSize = clientUDPSize
}

func Resolve(msg *dns.Msg) (*dns.Msg, error) {
	msg.RecursionDesired = true

	resp, _, err := client.Exchange(msg, UpstreamAddr)
	if err != nil {
		return nil, fmt.Errorf("upstream exchange %s: %w", UpstreamAddr, err)
	}

	if resp == nil {
		return nil, fmt.Errorf("empty response from upstream")
	}

	slog.Debug("upstream response",
		"question", msg.Question,
		"rcode", resp.Rcode,
		"answer_count", len(resp.Answer),
	)

	return resp, nil
}

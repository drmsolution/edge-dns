package upstream

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/miekg/dns"
)

const UpstreamAddr = "1.1.1.1:53"

var client = &dns.Client{
	Net:     "udp",
	Timeout: 5 * time.Second,
	UDPSize: 1232,
}

func init() {
	if net.JoinHostPort("1.1.1.1", "53") != UpstreamAddr {
		panic("upstream address mismatch")
	}
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

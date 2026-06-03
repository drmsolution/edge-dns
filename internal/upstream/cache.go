package upstream

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/patrickmn/go-cache"
)

var responseCache *cache.Cache

func InitCache(defaultTTL, cleanupInterval time.Duration) {
	responseCache = cache.New(defaultTTL, cleanupInterval)
	slog.Info("upstream cache initialized", "default_ttl", defaultTTL)
}

func cacheKey(msg *dns.Msg) (string, bool) {
	if len(msg.Question) == 0 {
		return "", false
	}
	q := msg.Question[0]
	return fmt.Sprintf("%s:%s", strings.ToLower(q.Name), dns.TypeToString[q.Qtype]), true
}

func respMinTTL(resp *dns.Msg) time.Duration {
	min := int64(^uint32(0) >> 1)
	found := false
	for _, rr := range resp.Answer {
		if int64(rr.Header().Ttl) < min {
			min = int64(rr.Header().Ttl)
			found = true
		}
	}
	for _, rr := range resp.Ns {
		if int64(rr.Header().Ttl) < min {
			min = int64(rr.Header().Ttl)
			found = true
		}
	}
	if !found {
		return 0
	}
	return time.Duration(min) * time.Second
}

func ResolveCached(msg *dns.Msg) (*dns.Msg, error) {
	key, ok := cacheKey(msg)
	if ok && responseCache != nil {
		if val, found := responseCache.Get(key); found {
			cached := val.(*dns.Msg)
			resp := cached.Copy()
			slog.Debug("upstream cache hit", "key", key)
			return resp, nil
		}
	}

	resp, err := Resolve(msg)
	if err != nil {
		return nil, err
	}

	if ok && responseCache != nil && resp.Rcode == dns.RcodeSuccess {
		ttl := respMinTTL(resp)
		if ttl > 0 {
			responseCache.Set(key, resp.Copy(), ttl)
			slog.Debug("upstream cache set", "key", key, "ttl", ttl)
		}
	}

	return resp, nil
}

func ClearCache() {
	if responseCache != nil {
		responseCache.Flush()
		slog.Info("upstream cache flushed")
	}
}

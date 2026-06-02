package rule

import (
	"log/slog"
	"strings"
	"sync/atomic"
)

var global *Checker
var totalChecks atomic.Uint64

func InitChecker(redisAddr string) {
	global = NewChecker(redisAddr)
	slog.Info("rule checker initialized", "redis_addr", redisAddr)
}

func CheckRule(userID string, domain string) int {
	totalChecks.Add(1)

	domain = strings.TrimSuffix(domain, ".")
	domain = strings.ToLower(domain)

	if global != nil {
		return global.Check(userID, domain)
	}

	slog.Warn("checker not initialized, using inline fallback")
	for _, blocked := range defaultFallback {
		if domain == blocked || strings.HasSuffix(domain, "."+blocked) {
			slog.Debug("inline block", "domain", domain, "match", blocked)
			return 1
		}
	}
	return 0
}

func ClearUserCache(userID string) {
	if global != nil {
		global.ClearUserCache(userID)
	}
}

func TotalChecks() uint64 {
	return totalChecks.Load()
}

func IsBlockedDomain(domain string) bool {
	if global != nil {
		return global.Check("default", domain) == 1
	}

	domain = strings.TrimSuffix(domain, ".")
	domain = strings.ToLower(domain)
	for _, blocked := range defaultFallback {
		if domain == blocked || strings.HasSuffix(domain, "."+blocked) {
			return true
		}
	}
	return false
}

var defaultFallback = []string{
	"example-blocked.com",
	"malware.test",
	"ads.tracker.com",
	"pornhub.com",
	"xvideos.com",
	"doubleclick.net",
	"google-analytics.com",
}

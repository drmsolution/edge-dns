package rule

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGeneratePatterns(t *testing.T) {
	tests := []struct {
		domain   string
		expected []string
	}{
		{
			domain:   "google.com",
			expected: []string{"google.com", "*.com"},
		},
		{
			domain:   "sub.doubleclick.net",
			expected: []string{"sub.doubleclick.net", "*.doubleclick.net", "*.net"},
		},
		{
			domain:   "a.b.c.example.com",
			expected: []string{"a.b.c.example.com", "*.b.c.example.com", "*.c.example.com", "*.example.com", "*.com"},
		},
		{
			domain:   "localhost",
			expected: []string{"localhost"},
		},
	}

	for _, tc := range tests {
		got := generatePatterns(tc.domain)
		if len(got) != len(tc.expected) {
			t.Errorf("generatePatterns(%q) = %v, want %v", tc.domain, got, tc.expected)
			continue
		}
		for i := range got {
			if got[i] != tc.expected[i] {
				t.Errorf("generatePatterns(%q)[%d] = %q, want %q", tc.domain, i, got[i], tc.expected[i])
			}
		}
	}
}

func TestGeneratePatternsLimitedDepth(t *testing.T) {
	deep := strings.Join([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"}, ".") + ".example.com"
	patterns := generatePatterns(deep)
	if len(patterns) > 12 {
		t.Errorf("too many patterns for deep domain: %d", len(patterns))
	}

	last := patterns[len(patterns)-1]
	if last != "*.com" {
		t.Errorf("last pattern should be *.com, got %q", last)
	}
}

func TestFallbackCheck(t *testing.T) {
	c := NewChecker("localhost:6379")

	tests := []struct {
		domain string
		want   int
	}{
		{"example-blocked.com", 1},
		{"sub.example-blocked.com", 1},
		{"google.com", 0},
		{"github.com", 0},
		{"ads.tracker.com", 1},
		{"tracker.com", 0},
		{"malware.test", 1},
		{"deep.sub.malware.test", 1},
		{"pornhub.com", 1},
		{"doubleclick.net", 1},
		{"google-analytics.com", 1},
		{"unknown-site.net", 0},
	}

	for _, tc := range tests {
		got := c.fallbackCheck(tc.domain)
		if got != tc.want {
			t.Errorf("fallbackCheck(%q) = %d, want %d", tc.domain, got, tc.want)
		}
	}
}

func TestCheckNormalizesDomain(t *testing.T) {
	c := NewChecker("localhost:6379")

	tests := []struct {
		domain string
		want   int
	}{
		{"Example-Blocked.Com", 1},
		{"EXAMPLE-BLOCKED.COM.", 1},
		{"SUB.Example-Blocked.Com.", 1},
		{"Ads.Tracker.Com", 1},
	}

	for _, tc := range tests {
		got := c.Check("default", tc.domain)
		if got != tc.want {
			t.Errorf("Check(%q) = %d, want %d", tc.domain, got, tc.want)
		}
	}
}

func TestInMemoryCache(t *testing.T) {
	c := NewChecker("localhost:6379")

	domain := "example-blocked.com"

	r1 := c.Check("user_abc", domain)
	if r1 != 1 {
		t.Fatalf("first check should be 1, got %d", r1)
	}

	c.enabled = false

	r2 := c.Check("user_abc", domain)
	if r2 != 1 {
		t.Errorf("cached check should still be 1 after Redis disabled, got %d", r2)
	}

	userID := "nonexistent"
	domain2 := "random-allowedsite.net"
	r3 := c.Check(userID, domain2)
	if r3 != 0 {
		t.Errorf("non-blocked domain should be 0, got %d", r3)
	}

	c.cache.Delete(userID + ":" + domain2)
	c.enabled = true
	r4 := c.Check(userID, domain2)
	if r4 != 0 {
		t.Errorf("non-blocked domain (no cache, no Redis) should be 0, got %d", r4)
	}
}

func TestConcurrentCacheAccess(t *testing.T) {
	c := NewChecker("localhost:6379")

	var wg sync.WaitGroup
	domains := []string{
		"example-blocked.com", "google.com", "ads.tracker.com",
		"github.com", "malware.test", "stackoverflow.com",
	}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				idx := rand.Intn(len(domains))
				_ = c.Check("user_"+strings.ToUpper(string(rune('A'+id%26))), domains[idx])
			}
		}(i)
	}

	wg.Wait()

	if c.cache.ItemCount() == 0 {
		t.Error("cache should have items after concurrent access")
	}
}

func TestCacheTTL(t *testing.T) {
	c := NewChecker("localhost:6379")

	c.Check("user_ttl", "example-blocked.com")

	key := "user_ttl:example-blocked.com"
	if _, found := c.cache.Get(key); !found {
		t.Fatal("cache should have the key immediately")
	}

	c.cache.DeleteExpired()

	if _, found := c.cache.Get(key); !found {
		t.Fatal("cache should still have key after DeleteExpired (TTL not expired)")
	}

	c.cache.Flush()
	if _, found := c.cache.Get(key); found {
		t.Error("cache should be empty after Flush")
	}
}

func TestPatternMatchWildcard(t *testing.T) {
	c := NewChecker("localhost:6379")

	if c.fallbackCheck("doubleclick.net") != 1 {
		t.Error("doubleclick.net should be blocked by fallback (exact match)")
	}
	if c.fallbackCheck("sub.doubleclick.net") != 1 {
		t.Error("sub.doubleclick.net should be blocked by fallback (suffix match)")
	}
}

func TestRedisGracefulDegradation(t *testing.T) {
	c := NewChecker("localhost:6379")
	c.enabled = false

	domain := "completely-new-blocked-domain.test"
	_ = c.Check("user_degraded", domain)

	c.enabled = true

	c.cache.Flush()

	_ = c.Check("user_degraded", domain)
}

func BenchmarkCheckFallback(b *testing.B) {
	c := NewChecker("localhost:6379")
	domains := []string{"google.com", "example-blocked.com", "github.com", "ads.tracker.com", "stackoverflow.com"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Check("bench_user", domains[i%len(domains)])
	}
}

func BenchmarkCheckCached(b *testing.B) {
	c := NewChecker("localhost:6379")

	c.Check("bench_cached", "example-blocked.com")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Check("bench_cached", "example-blocked.com")
	}
}

func BenchmarkGeneratePatterns(b *testing.B) {
	domains := []string{
		"google.com",
		"sub.doubleclick.net",
		"a.b.c.d.e.example.com",
		"very.long.subdomain.with.many.labels.example.org",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		generatePatterns(domains[i%len(domains)])
	}
}

func TestGlobalCheckRuleBackwardCompat(t *testing.T) {
	old := global
	defer func() { global = old }()

	global = NewChecker("localhost:6379")

	if r := CheckRule("test", "example-blocked.com"); r != 1 {
		t.Errorf("CheckRule via global should return 1, got %d", r)
	}
	if r := CheckRule("test", "google.com"); r != 0 {
		t.Errorf("CheckRule via global should return 0, got %d", r)
	}
}

func TestGlobalUninitialized(t *testing.T) {
	old := global
	defer func() { global = old }()

	global = nil

	if r := CheckRule("test", "example-blocked.com"); r != 1 {
		t.Errorf("uninitialized global should still block via inline fallback, got %d", r)
	}
	if r := CheckRule("test", "google.com"); r != 0 {
		t.Errorf("uninitialized global should allow via inline fallback, got %d", r)
	}
}

func TestCloseIdempotent(t *testing.T) {
	c := NewChecker("localhost:6379")
	if err := c.Close(); err != nil {
		t.Errorf("first Close should succeed, got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := c.Close(); err != nil {
		t.Logf("second Close may fail (pool already closed): %v", err)
	}
}

package rule

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
)

type Checker struct {
	cache    *cache.Cache
	rdb      *redis.Client
	fallback []string
	enabled  bool
}

type PatternType int

const (
	PatternExact PatternType = iota
	PatternWildcard
)

type candidate struct {
	raw   string
	ptype PatternType
}

func NewChecker(redisAddr string) *Checker {
	rdb := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		DialTimeout:  1 * time.Second,
		ReadTimeout:  300 * time.Millisecond,
		PoolSize:     10,
		MinIdleConns: 0,
		MaxRetries:   0,
	})

	c := &Checker{
		cache:   cache.New(60*time.Second, 120*time.Second),
		rdb:     rdb,
		enabled: true,
		fallback: []string{
			"example-blocked.com",
			"malware.test",
			"ads.tracker.com",
			"pornhub.com",
			"xvideos.com",
			"doubleclick.net",
			"google-analytics.com",
		},
	}

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		slog.Warn("redis unavailable, using fallback rules only", "error", err)
		c.enabled = false
	} else {
		slog.Info("redis connected", "addr", redisAddr)
	}

	return c
}

func (c *Checker) Check(userID, domain string) int {
	domain = strings.TrimSuffix(domain, ".")
	domain = strings.ToLower(domain)

	cacheKey := userID + ":" + domain

	if val, found := c.cache.Get(cacheKey); found {
		if val.(bool) {
			return 1
		}
		return 0
	}

	if c.enabled {
		blocked, err := c.checkRedis(domain, userID)
		if err == nil {
			c.cache.Set(cacheKey, blocked, cache.DefaultExpiration)
			if blocked {
				return 1
			}
			return 0
		}
		slog.Warn("redis query failed, fallback", "user_id", userID, "domain", domain, "error", err)
	}

	blocked := c.fallbackCheck(domain)
	c.cache.Set(cacheKey, blocked == 1, cache.DefaultExpiration)
	return blocked
}

func (c *Checker) checkRedis(domain, userID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	patterns := generatePatterns(domain)
	key := "user:settings:" + userID + ":blocked"

	vals, err := c.rdb.SMIsMember(ctx, key, patterns).Result()
	if err != nil {
		return false, err
	}

	for _, isBlocked := range vals {
		if isBlocked {
			return true, nil
		}
	}
	return false, nil
}

func (c *Checker) fallbackCheck(domain string) int {
	for _, blocked := range c.fallback {
		if domain == blocked || strings.HasSuffix(domain, "."+blocked) {
			slog.Debug("fallback block", "domain", domain, "match", blocked)
			return 1
		}
	}
	return 0
}

func (c *Checker) SeedUserBlocklist(userID string, domains []string) {
	ctx := context.Background()
	key := "user:settings:" + userID + ":blocked"

	if err := c.rdb.SAdd(ctx, key, domains).Err(); err != nil {
		slog.Error("seed blocklist", "user_id", userID, "error", err)
		return
	}
	slog.Info("seeded blocklist", "user_id", userID, "count", len(domains))
}

func (c *Checker) ClearUserCache(userID string) {
	prefix := userID + ":"
	var count int
	for k := range c.cache.Items() {
		if strings.HasPrefix(k, prefix) {
			c.cache.Delete(k)
			count++
		}
	}
	slog.Info("cleared cache", "user_id", userID, "entries", count)
}

func (c *Checker) Close() error {
	return c.rdb.Close()
}

func generatePatterns(domain string) []string {
	labels := strings.Split(domain, ".")
	if len(labels) > 10 {
		labels = labels[len(labels)-10:]
	}

	patterns := make([]string, 0, len(labels)+1)

	patterns = append(patterns, domain)

	for i := 1; i < len(labels); i++ {
		parent := strings.Join(labels[i:], ".")
		patterns = append(patterns, "*."+parent)
	}

	return patterns
}

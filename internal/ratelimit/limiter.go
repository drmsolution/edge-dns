package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

var memberCounter atomic.Uint64

var allowScript = redis.NewScript(`
local key = KEYS[1]
local now_ms  = tonumber(ARGV[1])
local win_ms  = tonumber(ARGV[2])
local limit   = tonumber(ARGV[3])
local ttl_sec = tonumber(ARGV[4])
local member  = ARGV[5]
local min_score = now_ms - win_ms

redis.call('ZREMRANGEBYSCORE', key, 0, min_score)
local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now_ms, member)
    redis.call('EXPIRE', key, ttl_sec)
    return 1
else
    return 0
end
`)

type RateLimiter struct {
	client *redis.Client
}

func NewRateLimiter(client *redis.Client) *RateLimiter {
	return &RateLimiter{client: client}
}

func (rl *RateLimiter) AllowQuery(ctx context.Context, userID string, limit int, window time.Duration) (bool, error) {
	key := "dns:ratelimit:" + userID
	nowMs := time.Now().UnixMilli()
	winMs := window.Milliseconds()
	ttlSec := int64(math.Ceil(window.Seconds()))

	member := fmt.Sprintf("%d:%d", nowMs, memberCounter.Add(1))

	result, err := allowScript.Run(ctx, rl.client, []string{key}, nowMs, winMs, limit, ttlSec, member).Result()
	if err != nil {
		return false, err
	}
	return result.(int64) == 1, nil
}

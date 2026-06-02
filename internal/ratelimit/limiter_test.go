package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T) (*RateLimiter, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return NewRateLimiter(client), s
}

func TestAllowQuery_underLimit(t *testing.T) {
	rl, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		ok, err := rl.AllowQuery(ctx, "u1", 5, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestAllowQuery_overLimit(t *testing.T) {
	rl, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		ok, err := rl.AllowQuery(ctx, "u1", 5, time.Minute)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	ok, err := rl.AllowQuery(ctx, "u1", 5, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("6th request should be blocked")
	}
}

func TestAllowQuery_differentUsers(t *testing.T) {
	rl, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		rl.AllowQuery(ctx, "u1", 5, time.Minute)
	}

	ok, err := rl.AllowQuery(ctx, "u2", 5, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("different user should be allowed independently")
	}
}

func TestAllowQuery_windowSlides(t *testing.T) {
	rl, s := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		ok, err := rl.AllowQuery(ctx, "u1", 5, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	ok, err := rl.AllowQuery(ctx, "u1", 5, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("6th request should be blocked within window")
	}

	s.FastForward(11 * time.Second)

	ok, err = rl.AllowQuery(ctx, "u1", 5, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("request should be allowed after window slides")
	}
}

func TestAllowQuery_zeroLimit(t *testing.T) {
	rl, _ := newTestLimiter(t)
	ctx := context.Background()

	ok, err := rl.AllowQuery(ctx, "u1", 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("zero limit should block everything")
	}
}

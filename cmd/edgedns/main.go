package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/user/edge-dns/internal/analytics"
	"github.com/user/edge-dns/internal/handler"
	"github.com/user/edge-dns/internal/ratelimit"
	"github.com/user/edge-dns/internal/rule"
	"github.com/user/edge-dns/internal/server"
	dnsSync "github.com/user/edge-dns/internal/sync"
)

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

const (
	StdAddr        = ":8053"
	DoHAddr        = ":8443"
	DoTAddr        = ":8853"
	RedisAddr      = "localhost:6379"
	ClickHouseAddr = "localhost:9000"
	ClickHouseDB   = "default"
	ClickHouseUser = "default"
	ClickHousePass = ""
	MetricsAddr    = ":2112"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	rule.InitChecker(RedisAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	agg := analytics.NewLogAggregator(10000, 1000, 5*time.Second)
	handler.SetAggregator(agg)

	if err := agg.InitClickHouse(
		env("CLICKHOUSE_ADDR", ClickHouseAddr),
		env("CLICKHOUSE_DB", ClickHouseDB),
		env("CLICKHOUSE_USER", ClickHouseUser),
		env("CLICKHOUSE_PASS", ClickHousePass),
	); err != nil {
		slog.Warn("clickhouse not available, analytics will run without persistence",
			"error", err,
		)
	}

	agg.StartWorker(ctx)

	redisSyncClient := redis.NewClient(&redis.Options{
		Addr:        RedisAddr,
		DialTimeout: 3 * time.Second,
		PoolSize:    10,
	})
	defer redisSyncClient.Close()

	rl := ratelimit.NewRateLimiter(redisSyncClient)
	handler.SetRateLimiter(rl)

	dnsSync.StartSyncWorker(ctx, redisSyncClient, func(userID string) {
		rule.ClearUserCache(userID)
	})

	slog.Info("Edge DNS Resolver starting",
		"std_addr", StdAddr,
		"doh_addr", DoHAddr,
		"dot_addr", DoTAddr,
		"redis", RedisAddr,
	)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)

	wg.Add(4)
	go func() {
		defer wg.Done()
		if err := server.RunStd(ctx, StdAddr); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := server.RunDoH(ctx, DoHAddr); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := server.RunDoT(ctx, DoTAddr); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()

		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())

		srv := &http.Server{
			Addr:    env("METRICS_PORT", MetricsAddr),
			Handler: mux,
		}
		slog.Info("metrics HTTP server starting", "addr", srv.Addr)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case sig := <-sigCh:
		slog.Info("signal received, shutting down", "signal", sig)
		cancel()
	case err := <-errCh:
		slog.Error("server error", "error", err)
		cancel()
	}

	wg.Wait()
	agg.Wait()
	slog.Info("Edge DNS Resolver stopped")
}

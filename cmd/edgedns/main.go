package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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

func main() {
	logLevel := slog.LevelInfo
	switch env("LOG_LEVEL", "info") {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	stdAddr := env("STD_ADDR", ":8053")
	dohAddr := env("DOH_ADDR", ":8443")
	dotAddr := env("DOT_ADDR", ":8853")
	metricsAddr := env("METRICS_PORT", ":2112")
	redisAddr := env("REDIS_ADDR", "localhost:6379")
	chAddr := env("CLICKHOUSE_ADDR", "localhost:9000")
	chDB := env("CLICKHOUSE_DB", "default")
	chUser := env("CLICKHOUSE_USER", "default")
	chPass := env("CLICKHOUSE_PASS", "")
	if rlq := env("RATE_LIMIT_QUERIES", "100"); rlq != "" {
		if n, err := strconv.Atoi(rlq); err == nil && n > 0 {
			handler.SetRateLimit(n, time.Second)
		}
	}

	rule.InitChecker(redisAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	agg := analytics.NewLogAggregator(10000, 1000, 5*time.Second)
	handler.SetAggregator(agg)

	if err := agg.InitClickHouse(chAddr, chDB, chUser, chPass); err != nil {
		slog.Warn("clickhouse not available, analytics will run without persistence",
			"error", err,
		)
	}

	agg.StartWorker(ctx)

	redisSyncClient := redis.NewClient(&redis.Options{
		Addr:        redisAddr,
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
		"std_addr", stdAddr,
		"doh_addr", dohAddr,
		"dot_addr", dotAddr,
		"redis", redisAddr,
	)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)

	wg.Add(4)
	go func() {
		defer wg.Done()
		if err := server.RunStd(ctx, stdAddr); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := server.RunDoH(ctx, dohAddr); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := server.RunDoT(ctx, dotAddr); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()

		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := rule.PingRedis(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"status": "unhealthy", "error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			if err := rule.PingRedis(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"status": "not_ready", "error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
		})

		srv := &http.Server{
			Addr:    metricsAddr,
			Handler: mux,
		}
		slog.Info("metrics HTTP server starting", "addr", srv.Addr)

		shutdown := func() {
			shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			srv.Shutdown(shutdownCtx)
		}

		go func() {
			<-ctx.Done()
			shutdown()
		}()

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

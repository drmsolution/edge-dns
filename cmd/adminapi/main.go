package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/redis/go-redis/v9"
	"github.com/user/edge-dns/internal/admin"
)

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	redisAddr := env("REDIS_ADDR", "localhost:6379")
	chAddr := env("CLICKHOUSE_ADDR", "localhost:9000")
	chDB := env("CLICKHOUSE_DB", "default")
	chUser := env("CLICKHOUSE_USER", "default")
	chPass := env("CLICKHOUSE_PASS", "")
	listen := env("ADMIN_API_ADDR", ":8080")

	rdb := redis.NewClient(&redis.Options{
		Addr:        redisAddr,
		DialTimeout: 3 * time.Second,
		PoolSize:    10,
	})
	defer rdb.Close()

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		slog.Error("redis not available", "error", err)
		os.Exit(1)
	}
	slog.Info("redis connected", "addr", redisAddr)

	var chConn clickhouse.Conn
	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{chAddr},
		Auth: clickhouse.Auth{
			Database: chDB,
			Username: chUser,
			Password: chPass,
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		slog.Warn("clickhouse not available, analytics endpoints will be disabled",
			"error", err,
		)
	} else {
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := chConn.Ping(pingCtx); err != nil {
			slog.Warn("clickhouse ping failed", "error", err)
			chConn = nil
		} else {
			slog.Info("clickhouse connected", "addr", chAddr)
		}
		cancel()
	}

	svc := admin.New(rdb, chConn)
	router := svc.SetupRouter()

	slog.Info("admin API server starting", "addr", listen)

	go func() {
		if err := router.Run(listen); err != nil {
			slog.Error("admin API server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("signal received, shutting down", "signal", sig)

	if chConn != nil {
		chConn.Close()
	}
}

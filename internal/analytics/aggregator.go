package analytics

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/user/edge-dns/internal/metrics"
)

type LogAggregator struct {
	logCh          chan DNSLog
	batch          []DNSLog
	ticker         *time.Ticker
	flushThreshold int
	flushInterval  time.Duration
	droppedCount   atomic.Uint64
	flushedCount   atomic.Uint64
	chConn         clickhouse.Conn
	wg             sync.WaitGroup
}

func NewLogAggregator(bufferSize int, flushThreshold int, flushInterval time.Duration) *LogAggregator {
	return &LogAggregator{
		logCh:          make(chan DNSLog, bufferSize),
		batch:          make([]DNSLog, 0, flushThreshold),
		flushThreshold: flushThreshold,
		flushInterval:  flushInterval,
	}
}

func (a *LogAggregator) SubmitLog(log DNSLog) {
	select {
	case a.logCh <- log:
	default:
		a.droppedCount.Add(1)
	}
}

func (a *LogAggregator) InitClickHouse(addr, database, username, password string) error {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: database,
			Username: username,
			Password: password,
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		return err
	}

	a.chConn = conn
	slog.Info("clickhouse connected",
		"addr", addr,
		"database", database,
	)
	return nil
}

func (a *LogAggregator) StartWorker(ctx context.Context) {
	a.ticker = time.NewTicker(a.flushInterval)

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				a.flush()
				return

			case log := <-a.logCh:
				a.batch = append(a.batch, log)
				if len(a.batch) >= a.flushThreshold {
					a.flush()
				}

			case <-a.ticker.C:
				if len(a.batch) > 0 {
					a.flush()
				}
			}
		}
	}()

	slog.Info("analytics worker started",
		"batch_size", a.flushThreshold,
		"flush_interval", a.flushInterval,
		"channel_buffer", cap(a.logCh),
	)
}

func (a *LogAggregator) flush() {
	if len(a.batch) == 0 {
		return
	}

	batch := a.batch
	a.batch = make([]DNSLog, 0, a.flushThreshold)

	a.flushedCount.Add(uint64(len(batch)))
	a.flushBatch(batch)
}

func (a *LogAggregator) flushBatch(batch []DNSLog) {
	if a.chConn == nil {
		slog.Info("analytics batch skipped (clickhouse not configured)",
			"count", len(batch),
			"total_flushed", a.flushedCount.Load(),
			"total_dropped", a.droppedCount.Load(),
		)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	chBatch, err := a.chConn.PrepareBatch(ctx, "INSERT INTO dns_logs (timestamp, user_id, client_ip, domain, query_type, status, response_time_ns)")
	if err != nil {
		metrics.AnalyticsDroppedLogs.Add(float64(len(batch)))
		slog.Error("clickhouse prepare batch failed",
			"error", err,
			"batch_size", len(batch),
		)
		return
	}

	for _, log := range batch {
		if err := chBatch.Append(
			log.Timestamp,
			log.UserID,
			log.ClientIP,
			log.Domain,
			log.QueryType,
			string(log.Status),
			uint64(log.ResponseTime),
		); err != nil {
			slog.Error("clickhouse append row failed",
				"error", err,
				"domain", log.Domain,
			)
			continue
		}
	}

	if err := chBatch.Send(); err != nil {
		metrics.AnalyticsDroppedLogs.Add(float64(len(batch)))
		slog.Error("clickhouse batch send failed",
			"error", err,
			"batch_size", len(batch),
		)
		return
	}

	slog.Info("clickhouse batch flushed",
		"count", len(batch),
		"total_flushed", a.flushedCount.Load(),
		"total_dropped", a.droppedCount.Load(),
	)
}

func (a *LogAggregator) Stats() (flushed, dropped uint64) {
	return a.flushedCount.Load(), a.droppedCount.Load()
}

func (a *LogAggregator) Wait() {
	a.wg.Wait()
}

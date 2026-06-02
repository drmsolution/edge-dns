//go:build integration

package analytics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	chTests "github.com/testcontainers/testcontainers-go/modules/clickhouse"
)

func TestAnalyticsClickHouseIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("set INTEGRATION=1 to run this test")
	}

	ctx := context.Background()

	chContainer, err := chTests.RunContainer(ctx,
		chTests.WithUsername("default"),
		chTests.WithPassword(""),
		chTests.WithDatabase("default"),
	)
	if err != nil {
		t.Fatalf("failed to start clickhouse container: %v", err)
	}
	defer chContainer.Terminate(ctx)

	connHost, err := chContainer.ConnectionHost(ctx)
	if err != nil {
		t.Fatalf("failed to get connection host: %v", err)
	}

	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{connHost},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to connect for table creation: %v", err)
	}

	err = chConn.Exec(ctx, `CREATE TABLE IF NOT EXISTS dns_logs (
		timestamp     DateTime,
		user_id       String,
		client_ip     String,
		domain        String,
		query_type    String,
		status        String,
		response_time_ns UInt64
	) ENGINE = MergeTree()
	ORDER BY (timestamp, user_id)`)
	if err != nil {
		chConn.Close()
		t.Fatalf("failed to create dns_logs table: %v", err)
	}
	chConn.Close()

	agg := NewLogAggregator(1000, 5, time.Hour)
	if err := agg.InitClickHouse(connHost, "default", "default", ""); err != nil {
		t.Fatalf("InitClickHouse failed: %v", err)
	}

	aggCtx, cancel := context.WithCancel(ctx)
	agg.StartWorker(aggCtx)

	for i := 0; i < 5; i++ {
		agg.SubmitLog(DNSLog{
			Timestamp:    time.Now(),
			UserID:       "integration_test",
			ClientIP:     "192.168.1.1",
			Domain:       "example.com",
			QueryType:    "A",
			Status:       StatusAllowed,
			ResponseTime: time.Millisecond,
		})
	}

	time.Sleep(2 * time.Second)

	cancel()
	agg.Wait()

	verifyConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{connHost},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to open verify connection: %v", err)
	}
	defer verifyConn.Close()

	var count uint64
	if err := verifyConn.QueryRow(ctx, "SELECT COUNT(*) FROM dns_logs").Scan(&count); err != nil {
		t.Fatalf("SELECT COUNT(*) failed: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 rows in dns_logs, got %d", count)
	}

	t.Logf("SUCCESS: dns_logs contains %d rows as expected", count)
}

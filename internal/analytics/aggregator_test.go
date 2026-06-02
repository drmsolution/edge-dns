package analytics

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func makeLogs(n int) []DNSLog {
	logs := make([]DNSLog, n)
	for i := range logs {
		logs[i] = DNSLog{
			Timestamp:    time.Now(),
			UserID:       "user_test",
			ClientIP:     "127.0.0.1",
			Domain:       "example.com",
			QueryType:    "A",
			Status:       StatusAllowed,
			ResponseTime: time.Millisecond,
		}
	}
	return logs
}

func TestNewAggregator(t *testing.T) {
	a := NewLogAggregator(100, 10, time.Second)
	if a == nil {
		t.Fatal("expected non-nil aggregator")
	}
	if cap(a.logCh) != 100 {
		t.Errorf("buffer size = %d, want 100", cap(a.logCh))
	}
}

func TestSubmitLogNonBlocking(t *testing.T) {
	a := NewLogAggregator(5, 100, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	for i := 0; i < 1000; i++ {
		a.SubmitLog(makeLogs(1)[0])
	}

	cancel()
	a.Wait()

	dropped := a.droppedCount.Load()
	t.Logf("submitted 1000, dropped: %d (buffer was %d)", dropped, cap(a.logCh))

	if dropped > 995 {
		t.Errorf("too many drops: %d/1000", dropped)
	}
}

func TestBatchFlushOnThreshold(t *testing.T) {
	a := NewLogAggregator(1000, 50, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	logs := makeLogs(49)
	for i := range logs {
		a.SubmitLog(logs[i])
	}
	time.Sleep(100 * time.Millisecond)
	flushed1, _ := a.Stats()
	if flushed1 > 0 {
		t.Fatalf("expected no flush at 49 logs, got %d", flushed1)
	}

	a.SubmitLog(makeLogs(1)[0])
	time.Sleep(100 * time.Millisecond)
	flushed2, _ := a.Stats()
	if flushed2 == 0 {
		t.Fatal("expected flush at 50 logs, got 0")
	}

	cancel()
	a.Wait()
}

func TestBatchFlushOnInterval(t *testing.T) {
	a := NewLogAggregator(1000, 1000, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	logs := makeLogs(5)
	for i := range logs {
		a.SubmitLog(logs[i])
	}

	time.Sleep(50 * time.Millisecond)
	flushed1, _ := a.Stats()
	if flushed1 > 0 {
		t.Fatalf("expected no flush before interval, got %d", flushed1)
	}

	time.Sleep(100 * time.Millisecond)
	flushed2, _ := a.Stats()
	if flushed2 == 0 {
		t.Fatal("expected flush after interval, got 0")
	}

	cancel()
	a.Wait()
}

func TestMultipleBatches(t *testing.T) {
	a := NewLogAggregator(10000, 50, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	total := 1000
	logs := makeLogs(total)
	for i := range logs {
		a.SubmitLog(logs[i])
	}

	time.Sleep(500 * time.Millisecond)
	flushed, dropped := a.Stats()
	t.Logf("total=%d flushed=%d dropped=%d", total, flushed, dropped)

	if flushed == 0 {
		t.Fatal("expected at least one flush")
	}
	if flushed < uint64(total)-dropped-50 {
		t.Errorf("missing logs: flushed=%d total=%d dropped=%d", flushed, total, dropped)
	}

	cancel()
	a.Wait()
}

func TestFlushOnShutdown(t *testing.T) {
	a := NewLogAggregator(1000, 1000, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	logs := makeLogs(10)
	for i := range logs {
		a.SubmitLog(logs[i])
	}

	time.Sleep(50 * time.Millisecond)

	cancel()
	a.Wait()

	flushed, _ := a.Stats()
	if flushed == 0 {
		t.Fatal("expected flush on shutdown, got 0")
	}
	t.Logf("flushed %d logs on shutdown", flushed)
}

func TestSubmitConcurrent(t *testing.T) {
	a := NewLogAggregator(10000, 100, 200*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	var prodWg sync.WaitGroup
	nProducers := 10
	logsPerProducer := 200

	for p := 0; p < nProducers; p++ {
		prodWg.Add(1)
		go func(id int) {
			defer prodWg.Done()
			for i := 0; i < logsPerProducer; i++ {
				a.SubmitLog(DNSLog{
					Timestamp:    time.Now(),
					UserID:       "user_test",
					ClientIP:     "127.0.0.1",
					Domain:       "example.com",
					QueryType:    "A",
					Status:       StatusAllowed,
					ResponseTime: time.Millisecond,
				})
			}
		}(p)
	}
	prodWg.Wait()

	time.Sleep(500 * time.Millisecond)

	cancel()
	a.Wait()

	flushed, dropped := a.Stats()
	totalSubmitted := uint64(nProducers * logsPerProducer)
	accounted := flushed + dropped
	t.Logf("submitted=%d flushed=%d dropped=%d accounted=%d",
		totalSubmitted, flushed, dropped, accounted)

	if totalSubmitted != accounted {
		t.Errorf("inconsistent: submitted=%d flushed=%d dropped=%d sum=%d",
			totalSubmitted, flushed, dropped, accounted)
	}
}

func TestNoFlushOnEmptyBatch(t *testing.T) {
	var logBuf syncBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	a := NewLogAggregator(100, 50, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	a.StartWorker(ctx)

	time.Sleep(200 * time.Millisecond)

	cancel()
	a.Wait()

	if logBuf.contains("flushed") {
		t.Error("expected no flush for empty batch")
	}
}

type syncBuf struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuf) contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Contains(string(b.buf), s)
}

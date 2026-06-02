package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type CacheEvictor func(userID string)

type Worker struct {
	rdb     *redis.Client
	evictor CacheEvictor
	logger  *slog.Logger
}

func StartSyncWorker(ctx context.Context, rdb *redis.Client, evictor CacheEvictor) {
	w := &Worker{
		rdb:     rdb,
		evictor: evictor,
		logger:  slog.With("component", "sync"),
	}
	go w.run(ctx)
}

func (w *Worker) run(ctx context.Context) {
	backoff := 500 * time.Millisecond
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("sync worker stopped")
			return
		default:
			w.subscribe(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (w *Worker) subscribe(ctx context.Context) {
	pubsub := w.rdb.Subscribe(ctx, ConfigChannel)
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		w.logger.Warn("subscribe failed, will retry", "error", err)
		return
	}

	w.logger.Info("subscribed to config channel",
		"channel", ConfigChannel,
	)

	ch := pubsub.Channel(
		redis.WithChannelSize(100),
		redis.WithChannelHealthCheckInterval(10*time.Second),
	)

	for msg := range ch {
		w.handleMessage(msg.Payload)
	}

	w.logger.Warn("pubsub channel closed, reconnecting...")
}

func (w *Worker) handleMessage(payload string) {
	event, err := DecodeEvent(payload)
	if err != nil {
		w.logger.Warn("received invalid event", "payload", payload, "error", err)
		return
	}

	w.logger.Info("received config event",
		"user_id", event.UserID,
		"action", event.Action,
	)

	switch event.Action {
	case ActionClearCache:
		w.evictor(event.UserID)
	default:
		w.logger.Warn("unhandled action", "action", event.Action)
	}
}

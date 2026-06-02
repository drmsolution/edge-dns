package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/edge-dns/internal/sync"
)

func main() {
	redisAddr := flag.String("redis", "localhost:6379", "Redis address")
	userID := flag.String("user", "", "User ID to target")
	action := flag.String("action", "clear_cache", "Action: clear_cache | update_rule")
	flag.Parse()

	if *userID == "" {
		fmt.Println("Usage: publisher --user <user_id> [--action clear_cache|update_rule] [--redis addr:port]")
		fmt.Println()
		fmt.Println("Publishes a configuration change event to Redis Pub/Sub.")
		fmt.Println("The Edge DNS server listens for this event and evicts")
		fmt.Println("the user's in-memory cache, forcing a fresh Redis lookup.")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  publisher --user user_abc123")
		fmt.Println("  publisher --user user_abc123 --action update_rule")
		fmt.Println("  publisher --user user_xyz --redis 10.0.0.5:6379")
		os.Exit(1)
	}

	event := sync.ConfigEvent{
		UserID: *userID,
		Action: *action,
	}

	if err := event.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid event: %v\n", err)
		os.Exit(1)
	}

	data, _ := json.Marshal(event)
	fmt.Printf("Publishing: %s\n", string(data))

	rdb := redis.NewClient(&redis.Options{
		Addr:        *redisAddr,
		DialTimeout: 3 * time.Second,
	})
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to Redis at %s: %v\n", *redisAddr, err)
		os.Exit(1)
	}

	count, err := rdb.Publish(ctx, sync.ConfigChannel, string(data)).Result()
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Event published to channel %q.\n", sync.ConfigChannel)
	fmt.Printf("  user_id: %s\n", event.UserID)
	fmt.Printf("  action:  %s\n", event.Action)
	fmt.Printf("  receivers: %d (0 = no subscriber — start the DNS server first)\n", count)
}

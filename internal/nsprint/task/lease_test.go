package task_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
)

// redisNowMS reads Redis server TIME, the same clock the transition functions
// use. The lease controls backdate stored timestamps against it; no wall-clock
// sleep decides an expiry (spec 2.1 rule 3).
func redisNowMS(t *testing.T, ctx context.Context, client *redis.Client) int64 {
	t.Helper()
	raw, err := client.Do(ctx, "TIME").Result()
	if err != nil {
		t.Fatalf("redis TIME: %v", err)
	}
	parts, ok := raw.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("redis TIME reply = %T; want two parts", raw)
	}
	seconds, err := strconv.ParseInt(fmt.Sprint(parts[0]), 10, 64)
	if err != nil {
		t.Fatalf("redis TIME seconds %v: %v", parts[0], err)
	}
	micros, err := strconv.ParseInt(fmt.Sprint(parts[1]), 10, 64)
	if err != nil {
		t.Fatalf("redis TIME micros %v: %v", parts[1], err)
	}
	return seconds*1000 + micros/1000
}

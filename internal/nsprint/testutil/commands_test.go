package testutil

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The counter is not vacuous: a client that sends a PING is counted.
func TestCommandCounterCountsAPing(t *testing.T) {
	addr, count := CommandCounter(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if n := count(); n != 0 {
		t.Fatalf("NewClient sent %d commands; want 0", n)
	}
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if n := count(); n < 1 {
		t.Fatalf("after a PING the counter reads %d; want at least 1", n)
	}
}

package testutil

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The counter is not vacuous: a client that sends a PING is counted.
func TestCommandCounterCountsAPing(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if n := count(); n != 0 {
		require.Equal(t, int64(0), n, "NewClient sent %d commands; want 0", n)
	}
	if err := c.Ping(context.Background()).Err(); err != nil {
		require.NoError(t, err, "ping: %v", err)
	}
	if n := count(); n < 1 {
		require.GreaterOrEqual(t, n, int64(1), "after a PING the counter reads %d; want at least 1", n)
	}
}

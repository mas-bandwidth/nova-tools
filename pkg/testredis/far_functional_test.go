//go:build functional

package testredis

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The distance through a real redis-server: the spec of Far, at the 100ms it
// names. One server serves all three, as a package's functional tests share one,
// and the three run one after the other on one connection so each sees only its
// own writes in the ledger.
//
// WHAT IS ASSERTED HERE. The events, exactly: the proxy counts every write it
// held and forwarded and the least time any was held by its own clock, and a
// client that waits for each reply cannot go faster than the holds it is behind,
// so "a pipeline of a hundred pays one delay and not a hundred, three commands
// pay three" is the count of writes and the shortest hold. The count is exact at
// any load. The times a client measured are logged here, for a reader who wants
// the numbers.
//
// THE WALL CLOCK is asserted too, in tools/fardelay/distance_functional_test.go:
// one PING takes at least the delay and at most the delay plus a slack, and so on.
// It lives there because internal/ci's waits class (docs/SPEC-CI.md) reads every
// _test.go under internal/ and cmd/ and refuses a comparison with elapsed time.

// farStoreDelay is the distance of these tests.
const farStoreDelay = 100 * time.Millisecond

// farOneWrite is what a single command, or a whole pipeline, is at the proxy.
// The length of the pipeline and the commands sent one after the other are
// farPipelined and farSeparate, in far_test.go.
const farOneWrite = 1

// farStoreBound bounds each test's own calls, generously.
const farStoreBound = 30 * time.Second

// farClient is a client of addr as the tools open one: RESP3, no identity and no
// maintenance notifications, one connection, no retry. It is warmed, so the
// connection and its handshake are not what the next command pays for.
func farClient(ctx context.Context, t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:            addr,
		Protocol:        3,
		DisableIdentity: true,
		MaxRetries:      -1,
		DialerRetries:   1,
		PoolSize:        1,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode:         maintnotifications.ModeDisabled,
			EndpointType: maintnotifications.EndpointTypeNone,
		},
	})
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Ping(ctx).Err(); err != nil {
		require.NoError(t, err, "warming the connection through Far: %v", err)
	}
	return c
}

func TestFarThroughARealStore(t *testing.T) {
	t.Parallel()

	store := Start(t)
	link := FarLink(t, store, farStoreDelay)
	ctx, cancel := context.WithTimeout(t.Context(), farStoreBound)
	t.Cleanup(cancel)
	c := farClient(ctx, t, link.Addr())

	// held asserts what a run of n writes left in the ledger: n writes, none held
	// less than the distance.
	held := func(t *testing.T, before, n int, what string) {
		t.Helper()
		if got := link.Writes() - before; got != n {
			assert.Equal(t, n, got, "%s was %d writes at the proxy; want %d, one delay for each", what, got, n)
		}
		if got := link.Shortest(); got < farStoreDelay {
			assert.Failf(t, "", "%s: the proxy held a write for %v; want none held less than %v", what, got, farStoreDelay)
		}
	}

	t.Run("one PING pays the delay once", func(t *testing.T) {
		before := link.Writes()
		start := time.Now()
		got, err := c.Ping(ctx).Result()
		took := time.Since(start)
		if err != nil || got != "PONG" {
			require.Failf(t, "", "PING = %q, %v", got, err)
		}
		held(t, before, farOneWrite, "one PING")
		t.Logf("one PING through %v of distance: %v as the client saw it", farStoreDelay, took)
	})

	t.Run("a pipeline of a hundred PINGs pays it once", func(t *testing.T) {
		before := link.Writes()
		start := time.Now()
		pipe := c.Pipeline()
		cmds := make([]*redis.StatusCmd, farPipelined)
		for i := range cmds {
			cmds[i] = pipe.Ping(ctx)
		}
		_, err := pipe.Exec(ctx)
		took := time.Since(start)
		if err != nil {
			require.NoError(t, err, "pipeline: %v", err)
		}
		for i, cmd := range cmds {
			if got := cmd.Val(); got != "PONG" {
				require.Equal(t, "PONG", got, "command %d of the pipeline answered %q; want PONG", i, got)
			}
		}
		held(t, before, farOneWrite, "a pipeline of a hundred PINGs")
		t.Logf("a pipeline of %d PINGs through %v of distance: %v as the client saw it", farPipelined, farStoreDelay, took)
	})

	t.Run("three separate commands pay it three times", func(t *testing.T) {
		before := link.Writes()
		start := time.Now()
		for i := 0; i < farSeparate; i++ {
			if got, err := c.Ping(ctx).Result(); err != nil || got != "PONG" {
				require.Failf(t, "", "PING %d = %q, %v", i, got, err)
			}
		}
		took := time.Since(start)
		held(t, before, farSeparate, "three separate commands")
		t.Logf("%d separate PINGs through %v of distance: %v as the client saw it", farSeparate, farStoreDelay, took)
	})

	t.Run("a write through Far lands in the store", func(t *testing.T) {
		const key, value = "far:key", "far value"
		if err := c.Set(ctx, key, value, 0).Err(); err != nil {
			require.NoError(t, err, "SET through Far: %v", err)
		}
		direct := redis.NewClient(&redis.Options{Addr: store, MaxRetries: -1, DialerRetries: 1})
		t.Cleanup(func() { _ = direct.Close() })
		if got, err := direct.Get(ctx, key).Result(); err != nil || got != value {
			require.Failf(t, "", "the store, read directly, holds %q, %v; want %q", got, err, value)
		}
		if got, err := c.Get(ctx, key).Result(); err != nil || got != value {
			require.Failf(t, "", "GET through Far = %q, %v; want %q", got, err, value)
		}
	})
}

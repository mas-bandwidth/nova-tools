//go:build functional

package friend

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type funcDeliver struct{}

func (funcDeliver) Deliver(context.Context, string) (int, error) { return 0, nil }

// TestDaemonWritesPresenceOnTheBusStore starts one daemon on a throwaway
// redis-server (not a fixed port) and reads bus2:presence:<name> within a
// second of the loop, with a minute expiry armed. It does not wait out that
// minute. The unit gate does not build this file.
func TestDaemonWritesPresenceOnTheBusStore(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	require.NoError(t, c.SAdd(ctx, "friends", "bob").Err())
	st := bus.Redis{C: c, Timeout: 2 * time.Second}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 2, Store: st,
		Deliver: funcDeliver{}, Now: time.Now,
		Pause: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
			}
		},
		Record: func(string) {},
		Status: func(Status) error { return nil },
	}
	errc := make(chan error, 1)
	go func() { errc <- d.Run(runCtx) }()
	deadline := time.Now().Add(2 * time.Second)
	var got map[string]string
	for time.Now().Before(deadline) {
		marks, err := st.Marks(ctx, PresenceKey("bob"))
		if err == nil && len(marks) > 0 && marks[0]["seen"] != "" {
			got = marks[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-errc:
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon did not stop")
	}
	require.NotEmpty(t, got["seen"])
	assert.Equal(t, "bob", got["name"])
	ttl := c.PTTL(ctx, PresenceKey("bob")).Val()
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, PresenceTTL)
}

//go:build functional

package friend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spend cap on a private server: the daemon's whole loop runs over a
// throwaway redis-server of its own (testredis, loopback only, nothing kept)
// with a fake harness that reports its usage the way a real one does, so the
// cap is measured, never estimated (docs/SPEC-FRIEND.md, spend caps). No
// secret and no real provider reach the harness: the environment carries
// nothing but what the store's address needs.
func redisBus(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.SAdd(context.Background(), "friends", "ada", "bob").Err())
	return &bus.Bus{Store: bus.Redis{C: c}}, c
}

// redisHarness is the daemon's delivery seam: a fake harness whose turns
// report their usage (SpentUSD) as a real harness's own record does.
type redisHarness struct {
	mu    sync.Mutex
	spent float64
	texts []string
	err   error
}

func (h *redisHarness) Deliver(_ context.Context, text string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.texts = append(h.texts, text)
	if h.err != nil {
		return 1, h.err
	}
	if h.spent == 0 {
		h.spent = 25 // the turn's usage report: over the $20/day cap
	}
	return 0, nil
}

func (h *redisHarness) SpentUSD() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.spent
}

func (h *redisHarness) runs() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.texts)
}

// redisDaemon is the daemon's loop over the private bus with a fake harness
// and a clock of its own: no real time, no socket but the loopback store.
func redisDaemon(t *testing.T, b *bus.Bus, h Deliverer) (*Daemon, context.Context, context.CancelFunc) {
	t.Helper()
	var mu sync.Mutex
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	beats := 0
	d := &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 4, Store: b.Store,
		noPresent: true, StepBeatForTests: true,
		Deliver: h,
		Seat:    func(context.Context) (string, error) { return "ada", nil },
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			now = now.Add(BeatEvery)
			return now
		},
		Pause: func(context.Context, time.Duration) {},
		Beat: func(context.Context, time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			beats++
			if beats >= 4 {
				cancel()
			}
			return nil
		},
		Record: func(string) {},
		Pong:   func() (Pong, bool, error) { return Pong{}, false, nil },
		Status: func(Status) error { return nil },
	}
	return d, ctx, cancel
}

// TestTheDaemonOnAPrivateServerStopsTurnsPastTheCapAndRaisesOneJudgment runs
// the daemon's loop over its own private redis-server with a fake harness that
// reports usage past the cap: the daemon launches no turn (the message stays
// pending) and raises one judgment naming the friend, the provider and the
// spend (docs/SPEC-FRIEND.md, spend caps). The scrubbed environment holds no
// provider secret, so a real harness would refuse; the fake reports its own
// usage report instead.
func TestTheDaemonOnAPrivateServerStopsTurnsPastTheCapAndRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	b, _ := redisBus(t)
	h := &redisHarness{spent: 25}
	var mu sync.Mutex
	var judged []string
	cap := &SpendCap{
		Friend:   "bob",
		Provider: "deepseek",
		Now:      time.Now,
		Cap:      func() float64 { return 20 },
		Spent:    func(time.Time) float64 { return h.SpentUSD() },
		Judgment: func(_ time.Time, spent, limit float64) string {
			subject, _ := SpendCapJudgmentText("bob", "deepseek", spent, limit)
			mu.Lock()
			judged = append(judged, subject)
			mu.Unlock()
			return subject
		},
	}
	d, ctx, cancel := redisDaemon(t, b, cap.Gate(h))
	defer cancel()
	_, err := b.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "hello", Body: "are you there?"})
	require.NoError(t, err)
	require.NoError(t, d.Run(ctx))

	assert.Zero(t, h.runs(), "past the cap the daemon launches no turn")
	pending, _, err := b.Peek(context.Background(), "bob")
	require.NoError(t, err)
	require.Len(t, pending, 1, "the message stays pending")
	mu.Lock()
	require.Len(t, judged, 1, "one judgment raised to the seat and the owner")
	assert.Contains(t, judged[0], "bob")
	assert.Contains(t, judged[0], "deepseek")
	assert.Contains(t, judged[0], "25")
	mu.Unlock()
}

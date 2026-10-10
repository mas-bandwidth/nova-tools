package friend

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresenceIsDownAtTenSeconds(t *testing.T) {
	t.Parallel()
	seen := t0
	fresh := map[string]string{"seen": seen.UTC().Format("2006-01-02T15:04:05.000Z07:00"), "asleep": "0"}
	assert.Equal(t, BusUp, PresenceState(fresh, seen.Add(9999*time.Millisecond)))
	assert.Equal(t, BusDown, PresenceState(fresh, seen.Add(DownAfter)))
	sleeping := map[string]string{"seen": fresh["seen"], "asleep": "1"}
	assert.Equal(t, BusAsleep, PresenceState(sleeping, seen.Add(9999*time.Millisecond)))
	assert.Equal(t, BusDown, PresenceState(sleeping, seen.Add(DownAfter)))
	assert.Equal(t, BusDown, PresenceState(nil, seen))
	assert.Equal(t, BusDown, PresenceState(map[string]string{}, seen))
	assert.Equal(t, BusDown, PresenceState(map[string]string{"seen": ""}, seen))
	broken := map[string]string{"seen": fresh["seen"], "asleep": "0", "broken": seen.UTC().Format(time.RFC3339)}
	assert.Equal(t, BusDown, PresenceState(broken, seen.Add(time.Second)))
}

func TestTheCoordinatorWritesOnlyProved(t *testing.T) {
	t.Parallel()
	st := bustest.NewFake(t0, "ada", "bob", "cara")
	ctx := context.Background()
	key := PresenceKey("bob")
	seed := map[string]string{
		"name": "bob", "seen": t0.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"instance": "i1", "harness": "fake", "route": "push", "asleep": "0",
		"queue": "1", "working": "0", "width": "4", "version": PresenceVersion,
		"kept": "yes",
	}
	require.NoError(t, st.PutHash(ctx, key, seed, PresenceTTL))
	until, ok := st.HashUntil(key)
	require.True(t, ok)
	assert.Equal(t, t0.Add(PresenceTTL), until)
	st.Advance(5 * time.Second)

	k := NewKeepalive()
	k.Friends(t0, []string{"bob", "cara"})
	k.Sent("bob", "n1", t0)
	k.Sent("cara", "n2", t0)
	require.True(t, k.Pong("bob", "n1", t0.Add(time.Second)))
	require.True(t, k.Pong("cara", "n2", t0.Add(time.Second)))
	require.NoError(t, k.HealthBatch(ctx, st, Authority{Name: "ada", Generation: "g7"}))

	got, err := st.Marks(ctx, key)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, t0.Add(time.Second).UTC().Format(time.RFC3339), got[0]["proved"])
	for f, v := range seed {
		assert.Equal(t, v, got[0][f], f)
	}
	_, gen := got[0]["generation"]
	assert.False(t, gen, "a generation is not stored")
	until2, ok := st.HashUntil(key)
	require.True(t, ok)
	assert.Equal(t, until, until2, "proved does not refresh the expiry")

	missing, err := st.Marks(ctx, PresenceKey("cara"))
	require.NoError(t, err)
	assert.Empty(t, missing[0], "no record is created for a friend that has none")

	st.Advance(PresenceTTL)
	require.NoError(t, k.HealthBatch(ctx, st, Authority{Name: "ada", Generation: "g7"}))
	gone, err := st.Marks(ctx, key)
	require.NoError(t, err)
	assert.Empty(t, gone[0], "a minute after the daemon's expiry, proved does not bring the record back")
}

type countingStore struct {
	*bustest.Fake
	mu    sync.Mutex
	seens []string
}

func (c *countingStore) PutHash(ctx context.Context, key string, fields map[string]string, ttl time.Duration) error {
	c.mu.Lock()
	c.seens = append(c.seens, fields["seen"])
	c.mu.Unlock()
	return c.Fake.PutHash(ctx, key, fields, ttl)
}

func TestPresenceHoldsWhileTheSprintServerHangs(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.StepBeatOffLoopForTests = true // a beat that hangs runs beside the step, never in it
	count := &countingStore{Fake: r.store}
	r.d.Store = count
	release := make(chan struct{})
	var closeOnce sync.Once
	finish := func() { closeOnce.Do(func() { close(release) }) }
	t.Cleanup(finish)
	var beats atomic.Int32
	r.d.Beat = func(ctx context.Context, _ time.Time) error {
		beats.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	}
	r.send(t, "ada", "hello", "are you there?")
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := r.d.Now
	var steps int
	r.d.Now = func() time.Time {
		now := base()
		steps++
		if steps >= 130 {
			cancel()
		}
		return now
	}
	errc := make(chan error, 1)
	go func() { errc <- r.d.Run(ctx) }()
	err, ok := recvWithin(context.Background(), errc, 30*time.Second)
	require.True(t, ok, "the daemon did not return while its beat hung")
	require.NoError(t, err)
	assert.Equal(t, int32(1), beats.Load(), "a beat that does not answer is not called again")
	finish()

	count.mu.Lock()
	seens := append([]string(nil), count.seens...)
	count.mu.Unlock()
	r.mu.Lock()
	var lines []string
	for _, line := range r.records {
		if strings.Contains(line, "beat: no answer within 900ms") {
			lines = append(lines, line)
		}
	}
	delivered := len(r.delivered)
	now := r.now
	r.mu.Unlock()

	require.GreaterOrEqual(t, len(seens), 100, "presence is written every step")
	for i := 1; i < len(seens); i++ {
		prev, err := time.Parse(time.RFC3339Nano, seens[i-1])
		require.NoError(t, err)
		cur, err := time.Parse(time.RFC3339Nano, seens[i])
		require.NoError(t, err)
		assert.Equal(t, time.Second, cur.Sub(prev), "seen moves one second a step")
	}
	require.GreaterOrEqual(t, len(lines), 2, "one beat line a minute")
	var prev time.Time
	for i, line := range lines {
		at, err := time.Parse(time.RFC3339, strings.TrimSuffix(line, " beat: no answer within 900ms"))
		require.NoError(t, err)
		if i > 0 {
			assert.GreaterOrEqual(t, at.Sub(prev), time.Minute)
		}
		prev = at
	}
	assert.Equal(t, 1, delivered, "a delivery is not held for the beat")
	assert.Contains(t, strings.Join(r.adaGot(t), "\n"), "daemon-pong n1")

	got, err := r.store.Marks(context.Background(), PresenceKey("bob"))
	require.NoError(t, err)
	require.NotEmpty(t, got[0]["seen"])
	assert.Equal(t, "0", got[0]["asleep"])
	assert.Equal(t, "push", got[0]["route"])
	assert.Equal(t, BusUp, PresenceState(got[0], now))
	_, proved := got[0]["proved"]
	assert.False(t, proved)
}

// The Redis check of this record is TestDaemonWritesPresenceOnTheBusStore,
// build tag functional. It is not part of the unit gate and was not run here.
var _ bus.Store = (*countingStore)(nil)

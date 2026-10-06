package friend

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putCount counts presence writes. The loop is the only caller.
type putCount struct {
	*bustest.Fake
	n atomic.Int32
}

func (p *putCount) PutHash(ctx context.Context, key string, fields map[string]string, ttl time.Duration) error {
	p.n.Add(1)
	return p.Fake.PutHash(ctx, key, fields, ttl)
}

// A beat that never answers does not hold a delivery, a pong, or a presence
// write. The clock is the rig's, one second a step, and the beat blocks on a
// channel. 130 steps is two minutes and ten seconds: three record lines, one
// a minute from the second step, and a presence write every step.
func TestPresenceHoldsWhileTheSprintServerHangs(t *testing.T) {
	t.Parallel()
	const steps = 130
	r := newRig(t)
	store := &putCount{Fake: r.store}
	r.d.Store = store
	r.send(t, "ada", "hello", "are you there?")
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.mu.Lock()
	r.pong = Pong{Nonce: "n1", At: t0, To: "ada"}
	r.pongSet = true
	r.mu.Unlock()

	entered := make(chan struct{})
	hold := make(chan struct{})
	r.d.Beat = func(context.Context, time.Time) error {
		close(entered)
		<-hold
		return nil
	}
	r.d.BeatWait = func() { <-entered }

	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.d.Now = func() time.Time {
		r.mu.Lock()
		r.now = r.now.Add(BeatEvery)
		now := r.now
		calls++
		// Start is the first call. The call that cancels returns before the
		// presence write, so the cancel sits one call past the last write.
		stop := calls >= steps+2
		r.mu.Unlock()
		if stop {
			cancel()
		}
		return now
	}
	require.NoError(t, r.d.Run(ctx))
	close(hold)

	require.NotEmpty(t, r.delivered, "the turn started while the beat was still unanswered")
	assert.Contains(t, strings.Join(r.adaGot(t), "\n"), "daemon-pong")
	assert.GreaterOrEqual(t, r.last().Pongs, 1, "the session pong was read while the beat was unanswered")
	assert.Equal(t, int32(steps), store.n.Load(), "one presence write a step")
	assert.Zero(t, r.beats, "the unanswered beat never finished")
	assert.Zero(t, r.last().Beats)

	hung := 0
	for _, line := range r.records {
		if strings.Contains(line, "beat: no answer within 900ms") {
			hung++
		}
	}
	assert.Equal(t, 3, hung, "one line a minute, the first once the deadline has passed: %v", r.records)

	got, err := r.store.Marks(context.Background(), PresenceKey("bob"))
	require.NoError(t, err)
	fields := got[0]
	require.NotEmpty(t, fields, "the last write is still inside its minute")
	assert.Equal(t, "0", fields["asleep"])
	assert.Equal(t, "push", fields["route"])
	assert.Equal(t, PresenceVersion, fields["version"])
	assert.NotContains(t, fields, "proved")
	seen, err := time.Parse(PresenceSeen, fields["seen"])
	require.NoError(t, err)
	assert.Equal(t, PresenceUp, PresenceState(fields, seen))
	assert.Equal(t, PresenceDown, PresenceState(fields, seen.Add(DownAfter)))
}

// At exactly DownAfter the record is down. Inside that window it is up or
// asleep, and a broken session is down however fresh seen is. A missing
// record is down.
func TestPresenceIsDownAtTenSeconds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	seen := func(d time.Duration) string { return now.Add(d).UTC().Format(PresenceSeen) }
	fresh := map[string]string{"seen": seen(0), "asleep": "0"}
	cases := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"no record", nil, PresenceDown},
		{"empty record", map[string]string{}, PresenceDown},
		{"fresh and awake", fresh, PresenceUp},
		{"fresh and asleep", map[string]string{"seen": seen(0), "asleep": "1"}, PresenceAsleep},
		{"one millisecond inside", map[string]string{"seen": seen(-DownAfter + time.Millisecond), "asleep": "0"}, PresenceUp},
		{"asleep inside the window", map[string]string{"seen": seen(-DownAfter + time.Millisecond), "asleep": "1"}, PresenceAsleep},
		{"exactly ten seconds", map[string]string{"seen": seen(-DownAfter), "asleep": "0"}, PresenceDown},
		{"exactly ten seconds and asleep", map[string]string{"seen": seen(-DownAfter), "asleep": "1"}, PresenceDown},
		{"older than ten seconds", map[string]string{"seen": seen(-DownAfter - time.Second), "asleep": "0"}, PresenceDown},
		{"broken and fresh", map[string]string{"seen": seen(0), "asleep": "0", "broken": now.Format(time.RFC3339)}, PresenceDown},
		{"unreadable seen", map[string]string{"seen": "yesterday", "asleep": "0"}, PresenceDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, PresenceState(c.fields, now))
		})
	}
}

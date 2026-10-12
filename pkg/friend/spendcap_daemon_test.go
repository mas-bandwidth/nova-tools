package friend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spendRigHarness is the daemon's delivery seam under the spend cap: a fake
// harness whose turns report their usage (SpentUSD) the way a real harness
// does, so the cap is measured, never estimated. The daemon runs over the
// in-process bus store (bustest.Fake): no socket, no real time. The
// private-server run of this loop is in spendcap_redis_functional_test.go
// (the functional tier).
type spendRigHarness struct {
	*rig
	mu    sync.Mutex
	spent float64
}

func (h *spendRigHarness) Deliver(ctx context.Context, text string) (int, error) {
	h.mu.Lock()
	h.spent = 25 // the current turn's usage report: over the $20/day cap
	h.mu.Unlock()
	return h.rig.Deliver(ctx, text)
}

func (h *spendRigHarness) SpentUSD() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.spent
}

// TestTheDaemonStopsTurnsPastTheCapAndRaisesOneJudgment runs the daemon's loop
// with a fake harness that reports usage past the cap: the daemon launches no
// turn (the message stays pending) and raises one judgment naming the friend,
// the provider and the spend (docs/SPEC-FRIEND.md, spend caps). It reaches the
// read gate (no build tag) and drives the wiring the daemon packages use, not
// the SpendCap's methods alone.
func TestTheDaemonStopsTurnsPastTheCapAndRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	h := &spendRigHarness{rig: r, spent: 25}
	var mu sync.Mutex
	var judged []string
	cap := &SpendCap{
		Friend:   "bob",
		Provider: "deepseek",
		Now:      r.d.Now,
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
	r.d.Deliver = cap.Gate(h)
	r.d.Pause = func(context.Context, time.Duration) {} // a deferral is not a turn: the rig's gate has no token to send
	r.send(t, "ada", "hello", "are you there?")
	r.run(t, 4)

	assert.Empty(t, r.delivered, "past the cap the daemon launches no turn")
	pending, _, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	require.Len(t, pending, 1, "the message stays pending")
	assert.Equal(t, "hello", pending[0].Message().Subject)
	mu.Lock()
	require.Len(t, judged, 1, "one judgment raised to the seat and the owner")
	assert.Contains(t, judged[0], "bob")
	assert.Contains(t, judged[0], "deepseek")
	assert.Contains(t, judged[0], "25")
	mu.Unlock()
}

// TestTheDaemonLaunchesNothingWhenIdle runs the daemon's loop with the spend
// cap reading idle: a friend with no dealt work and no live session launches
// no turn at all, the message staying pending (docs/SPEC-FRIEND.md, idle). The
// gate's idle seam answers the judgment it is handed: cmd/nova-friend's loop
// starts no batch, dealt, wake, lane or read turn with nothing dealt, and the
// cap's per-step Check stops a spent-out day even then; this test pins the
// seam's own answer, a friend with nothing dealt launching no turn.
func TestTheDaemonLaunchesNothingWhenIdle(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cap := &SpendCap{
		Friend:   "bob",
		Provider: "deepseek",
		Now:      r.d.Now,
		Idle:     func() bool { return true },
		Spent:    func(time.Time) float64 { return 0 },
	}
	r.d.Deliver = cap.Gate(r)
	r.d.Pause = func(context.Context, time.Duration) {} // no turn is launched, so the rig's gate has no token to send
	r.send(t, "ada", "hello", "are you there?")
	r.run(t, 2)
	assert.Empty(t, r.delivered, "idle launches no turn")
	pending, _, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	require.Len(t, pending, 1, "the message stays pending")
}

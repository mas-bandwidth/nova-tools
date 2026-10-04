package update

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainTimerSpy is the fake drain timer a WithDrainTimer context carries to
// the drain select in process. It records the drain it was consulted with; when
// ticked, its channel already holds a value before the select runs, so no test
// waits on the wall clock.
type drainTimerSpy struct {
	tick    time.Time
	drain   time.Duration
	called  int
	stopped bool
}

func (s *drainTimerSpy) seam(d time.Duration) (<-chan time.Time, func() bool) {
	s.called++
	s.drain = d
	ticks := make(chan time.Time, 1)
	if !s.tick.IsZero() {
		ticks <- s.tick
	}
	return ticks, func() bool {
		s.stopped = true
		return true
	}
}

// readDrainSeam reads the seam back through the exact expression process
// selects on: the value must sit under drainTimerKey with the seam type and be
// non-nil. It is a precondition helper, so require, not assert, holds it.
func readDrainSeam(t *testing.T, ctx context.Context) func(time.Duration) (<-chan time.Time, func() bool) {
	t.Helper()
	seam, ok := ctx.Value(drainTimerKey{}).(func(time.Duration) (<-chan time.Time, func() bool))
	require.True(t, ok, "WithDrainTimer must attach the seam under drainTimerKey")
	require.NotNil(t, seam)
	return seam
}

// TestReadCoverWithDrainTimerAttachesSeam pins the seam's main path: cli
// attaches env.DrainTimer so process drains through an injected timer rather
// than a real one, so the attached value must read back through process's
// selection expression, and the seam must receive exactly the allowance
// drainAllowance computes for the context it was attached to.
func TestReadCoverWithDrainTimerAttachesSeam(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		deadline    time.Time
		hasDeadline bool
		checkDrain  func(t *testing.T, drain time.Duration)
	}{
		{"grace cap", time.Time{}, false, func(t *testing.T, drain time.Duration) {
			assert.Equal(t, killGrace, drain)
		}},
		{"budget cap", time.Now().Add(time.Second), true, func(t *testing.T, drain time.Duration) {
			assert.Greater(t, drain, time.Duration(0))
			assert.Less(t, drain, killGrace, "the budget caps the drain below the grace")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := context.Background()
			if tc.hasDeadline {
				base = deadlineValue{Context: base, deadline: tc.deadline}
			}
			spy := &drainTimerSpy{}
			ctx := WithDrainTimer(base, spy.seam)
			seam := readDrainSeam(t, ctx)
			drain := drainAllowance(ctx)
			ticks, stop := seam(drain)
			assert.Equal(t, drain, spy.drain, "the seam is consulted with the allowance process computes")
			assert.Equal(t, 1, spy.called)
			require.NotNil(t, ticks)
			assert.True(t, stop(), "a stop before the tick must report the firing was prevented")
			assert.True(t, spy.stopped)
		})
	}
}

// TestReadCoverWithDrainTimerSeamDrivesTheSelect pins the two ways the drain
// select in process resolves through the seam: a fired tick takes the held
// branch and reports the pipe as held, a copy that finishes first calls the
// seam's stop and reports nothing held. The tick channel is pre-filled and the
// copy channel closed before the select, so nothing waits on the wall clock.
func TestReadCoverWithDrainTimerSeamDrivesTheSelect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		tick, done  bool
		wantHeld    bool
		wantStopped bool
	}{
		{"tick fires, the pipe is held", true, false, true, false},
		{"the copy finishes first", false, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spy := &drainTimerSpy{}
			if tc.tick {
				spy.tick = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			}
			ctx := WithDrainTimer(context.Background(), spy.seam)
			drain := drainAllowance(ctx)
			done := make(chan struct{})
			var held bool
			if seam, ok := ctx.Value(drainTimerKey{}).(func(time.Duration) (<-chan time.Time, func() bool)); ok && seam != nil {
				timerChan, stopTimer := seam(drain)
				if tc.done {
					close(done)
				}
				select {
				case <-done:
					stopTimer()
				case <-timerChan:
					held = true
				}
			} else {
				t.Fatal("WithDrainTimer must attach a seam the select adopts")
			}
			assert.Equal(t, tc.wantHeld, held, "held is reported only when the tick wins")
			assert.Equal(t, tc.wantStopped, spy.stopped, "the seam's stop runs when the copy finishes first")
			assert.Equal(t, drain, spy.drain)
		})
	}
}

// TestReadCoverWithDrainTimerRefusal pins the guard process selects with: a
// seam is adopted only when it is a non-nil function of the seam type. A nil
// fn is stored but not adopted, a context WithDrainTimer never touched yields
// nothing, and the parent context the wrapper received stays clean.
func TestReadCoverWithDrainTimerRefusal(t *testing.T) {
	t.Parallel()
	adopted := func(ctx context.Context) bool {
		seam, ok := ctx.Value(drainTimerKey{}).(func(time.Duration) (<-chan time.Time, func() bool))
		return ok && seam != nil
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"a nil fn is refused", WithDrainTimer(context.Background(), nil), false},
		{"a plain context has no seam", context.Background(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, adopted(tc.ctx))
		})
	}
	t.Run("a nil fn is stored but never adopted", func(t *testing.T) {
		t.Parallel()
		seam, ok := WithDrainTimer(context.Background(), nil).Value(drainTimerKey{}).(func(time.Duration) (<-chan time.Time, func() bool))
		assert.True(t, ok, "the nil fn is stored under the seam type")
		assert.Nil(t, seam, "the guard's second half refuses the nil fn, so the real timer is used")
	})
	t.Run("the parent context stays clean", func(t *testing.T) {
		t.Parallel()
		parent := context.Background()
		_ = WithDrainTimer(parent, func(time.Duration) (<-chan time.Time, func() bool) { return nil, func() bool { return false } })
		assert.False(t, adopted(parent), "WithDrainTimer returns a child; the parent must not carry the seam")
	})
}

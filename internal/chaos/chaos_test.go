package chaos

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is a controllable clock for testing.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time        { return f.now }
func (f *fakeClock) Sleep(d time.Duration) { f.now = f.now.Add(d) }

func TestHarnessRunsNamedFaultsWithinBounds(t *testing.T) {
	t.Parallel()

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("fault recovers inside bound passes", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: start}
		r := &Runner{Clock: clock}
		var recovered bool
		fault := Fault{
			Name:      "fast_recovery",
			Inject:    func() error { return nil },
			Heal:      func() { recovered = true },
			Recovered: func() bool { return clock.now.After(start.Add(10 * time.Second)) },
			Bound:     Bound(20 * time.Second),
		}
		report := r.runFault("suite", fault)
		assert.True(t, report.Recovered, "expected recovered=true, got %v", report.Recovered)
		assert.True(t, recovered, "heal was not called")
	})

	t.Run("fault recovers past bound fails", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: start}
		r := &Runner{Clock: clock}
		fault := Fault{
			Name:      "slow_recovery",
			Inject:    func() error { return nil },
			Heal:      func() {},
			Recovered: func() bool { return clock.now.After(start.Add(30 * time.Second)) },
			Bound:     Bound(20 * time.Second),
		}
		report := r.runFault("suite", fault)
		assert.False(t, report.Recovered, "expected recovered=false, got %v", report.Recovered)
	})

	t.Run("broken invariant fails", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: start}
		r := &Runner{
			Clock:     clock,
			Invariant: func() error { return errors.New("message lost") },
		}
		fault := Fault{
			Name:      "broken",
			Inject:    func() error { return nil },
			Heal:      func() {},
			Recovered: func() bool { return true },
			Bound:     Bound(20 * time.Second),
		}
		report := r.runFault("suite", fault)
		assert.False(t, report.Recovered, "expected recovered=false due to invariant, got %v", report.Recovered)
	})

	t.Run("multiple faults", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: start}
		r := &Runner{Clock: clock}
		faults := []Fault{
			{
				Name:      "f1",
				Inject:    func() error { return nil },
				Heal:      func() {},
				Recovered: func() bool { return clock.now.After(start.Add(5 * time.Second)) },
				Bound:     Bound(10 * time.Second),
			},
			{
				Name:      "f2",
				Inject:    func() error { return nil },
				Heal:      func() {},
				Recovered: func() bool { return clock.now.After(start.Add(16 * time.Second)) },
				Bound:     Bound(10 * time.Second),
			},
		}
		reports := r.Run("suite", faults...)
		require.Len(t, reports, 2, "expected 2 reports")
		assert.True(t, reports[0].Recovered, "f1 should have recovered")
		assert.False(t, reports[1].Recovered, "f2 should not have recovered (past bound)")
	})
}

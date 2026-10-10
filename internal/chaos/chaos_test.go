package chaos

import (
	"errors"
	"testing"
	"time"
)

// fakeClock is a controllable clock for testing.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time          { return f.now }
func (f *fakeClock) Sleep(d time.Duration)   { f.now = f.now.Add(d) }

func TestHarnessRunsNamedFaultsWithinBounds(t *testing.T) {
	t.Parallel()

	t.Run("fault recovers inside bound passes", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: time.Time{}}
		r := &Runner{Clock: clock}
		var recovered bool
		fault := Fault{
			Name: "fast_recovery",
			Inject: func() error { return nil },
			Heal: func() { recovered = true },
			Recovered: func() bool { return clock.now.After(time.Date(0, 1, 1, 0, 0, 10, 0, time.UTC)) },
			Bound: Bound(20 * time.Second),
		}
		report := r.runFault("suite", fault)
		if !report.Recovered {
			t.Errorf("expected recovered=true, got %v", report.Recovered)
		}
		if !recovered {
			t.Error("heal was not called")
		}
	})

	t.Run("fault recovers past bound fails", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: time.Time{}}
		r := &Runner{Clock: clock}
		fault := Fault{
			Name: "slow_recovery",
			Inject: func() error { return nil },
			Heal: func() {},
			Recovered: func() bool { return clock.now.After(time.Date(0, 1, 1, 0, 0, 30, 0, time.UTC)) },
			Bound: Bound(20 * time.Second),
		}
		report := r.runFault("suite", fault)
		if report.Recovered {
			t.Errorf("expected recovered=false, got %v", report.Recovered)
		}
	})

	t.Run("broken invariant fails", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: time.Time{}}
		r := &Runner{
			Clock: clock,
			Invariant: func() error { return errors.New("message lost") },
		}
		fault := Fault{
			Name: "broken",
			Inject: func() error { return nil },
			Heal: func() {},
			Recovered: func() bool { return true },
			Bound: Bound(20 * time.Second),
		}
		report := r.runFault("suite", fault)
		if report.Recovered {
			t.Errorf("expected recovered=false due to invariant, got %v", report.Recovered)
		}
	})

	t.Run("multiple faults", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{now: time.Time{}}
		r := &Runner{Clock: clock}
		faults := []Fault{
			{
				Name: "f1",
				Inject: func() error { return nil },
				Heal: func() {},
				Recovered: func() bool { return clock.now.After(time.Date(0, 1, 1, 0, 0, 5, 0, time.UTC)) },
				Bound: Bound(10 * time.Second),
			},
			{
				Name: "f2",
				Inject: func() error { return nil },
				Heal: func() {},
				Recovered: func() bool { return clock.now.After(time.Date(0, 1, 1, 0, 0, 15, 0, time.UTC)) },
				Bound: Bound(10 * time.Second),
			},
		}
		reports := r.Run("suite", faults...)
		if len(reports) != 2 {
			t.Fatalf("expected 2 reports, got %d", len(reports))
		}
		if !reports[0].Recovered {
			t.Error("f1 should have recovered")
		}
		if reports[1].Recovered {
			t.Error("f2 should not have recovered (past bound)")
		}
	})
}

package swarm

import (
	"testing"
	"time"
)

// fakeStaggerClock is one shared *time.Time plus a Sleep that advances it, so a Wait is
// observed on the counter, never on the machine's wall clock.
type fakeStaggerClock struct {
	now   time.Time
	slept time.Duration
}

func (c *fakeStaggerClock) Now() time.Time { return c.now }
func (c *fakeStaggerClock) Sleep(d time.Duration) {
	c.slept += d
	c.now = c.now.Add(d)
}

// TestBenchStaggerGapsTwoLaunchesToTheSameBench: the second Wait to one bench sleeps the
// remainder of the gap and no more, and different benches do not block each other.
func TestBenchStaggerGapsTwoLaunchesToTheSameBench(t *testing.T) {
	clk := &fakeStaggerClock{now: time.Unix(100, 0)}
	s := NewBenchStagger(3*time.Second, clk.Now, clk.Sleep)

	s.Wait("bench-a") // first: never blocks
	if clk.slept != 0 {
		t.Fatalf("the first Wait slept %s, want nothing", clk.slept)
	}
	clk.now = clk.now.Add(time.Second) // one second of real elapsed time

	s.Wait("bench-a") // second, one second later: sleeps the remaining two
	if clk.slept != 2*time.Second {
		t.Fatalf("the second Wait slept %s, want the 2s remainder of the gap", clk.slept)
	}

	// A different bench is a different lane: it never waits on bench-a's history.
	before := clk.slept
	s.Wait("bench-b")
	if clk.slept != before {
		t.Fatalf("a different bench slept %s, want nothing", clk.slept-before)
	}
}

// TestBenchStaggerZeroGapIsANoOp: gap <= 0 is off -- `--stagger 0` must never degrade to
// "stagger forever", and a caller that asked for no gap keeps today's behaviour.
func TestBenchStaggerZeroGapIsANoOp(t *testing.T) {
	clk := &fakeStaggerClock{now: time.Unix(100, 0)}
	s := NewBenchStagger(0, clk.Now, clk.Sleep)
	for i := 0; i < 10; i++ {
		s.Wait("bench-a")
	}
	if clk.slept != 0 {
		t.Fatalf("a zero-gap stagger slept %s, want nothing", clk.slept)
	}
}

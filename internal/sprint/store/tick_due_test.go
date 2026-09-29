package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// What is due is read from the state: a tick that did not finish it, or left
// it past a bound, leaves it for the next tick, and the machine line says it
// is catching up.

// landUnresolved lands a stream's queued cards as the merge step does, less
// the moves of the waiting primaries it would make ready: the state a landing
// whose resolve units were never applied leaves (a repair that skipped them).
// Only the tick's resolve can move those primaries on.
func (h *harness) landUnresolved(stream string) {
	h.t.Helper()
	step := MergeStep(sprint.MergeReq{Stream: stream, Batch: 100})
	plan := step.Plan
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		p := plan(s)
		var kept []sprint.Unit
		for _, u := range p.Units {
			if !strings.Contains(u.Moved, "waiting -> ready") {
				kept = append(kept, u)
			}
		}
		p.Units = kept
		return p
	}
	h.must(step)
}

func TestTheTickResolvesEveryWaiterOfOneNeedPastTheBound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"root"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: sprint.TickMaxMoves + 50, Needs: []string{"root"}}))
	h.through("root")
	h.landUnresolved("s1")
	if h.state("root") != sprint.Landed || len(h.snap().Work.Column(sprint.Waiting)) != sprint.TickMaxMoves+50 {
		t.Fatalf("the scene: root %s, waiting %d", h.state("root"), len(h.snap().Work.Column(sprint.Waiting)))
	}
	h.startMachine()
	res := h.machine()
	if res.Due != 50 {
		t.Fatalf("the first tick: due %d, want 50", res.Due)
	}
	if line := h.st.MachineLine(h.ctx); line != "machine: running (catching up: 50 moves due)" {
		t.Fatalf("the line after a bounded tick: %q", line)
	}
	h.tick(time.Second)
	res = h.machine()
	if n := len(h.snap().Work.Column(sprint.Waiting)); n != 0 {
		t.Fatalf("after the second tick: %d still waiting with their need landed", n)
	}
	if res.Due != 0 {
		t.Fatalf("the second tick: due %d", res.Due)
	}
	if line := h.st.MachineLine(h.ctx); line != "machine: running" {
		t.Fatalf("the line after catching up: %q", line)
	}
	h.clean("caught up")
}

// loseTickPart refuses the fence to one part of the tick, left times: other
// writers hold it every time the part tries.
type loseTickPart struct {
	*Mem
	verb string
	left int
}

func (l *loseTickPart) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if op.Verb == l.verb && l.left > 0 {
		l.left--
		return false, nil
	}
	return l.Mem.Acquire(ctx, gen, op)
}

func TestAResolveThatLostToOtherWritersIsDoneNextTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"b"}, Needs: []string{"a"}}))
	h.through("a")
	h.startMachine()
	h.machine()
	h.tick(time.Second)
	h.landUnresolved("s1")
	h.st.B = &loseTickPart{Mem: h.m, verb: "tick resolve", left: FenceTries}
	if _, err := h.st.Tick(h.ctx); err != nil {
		t.Fatalf("the contended tick: %v", err)
	}
	h.st.B = h.m
	if h.state("b") != sprint.Waiting {
		t.Fatalf("the scene: b %s", h.state("b"))
	}
	h.tick(time.Second)
	h.machine()
	if st := h.state("b"); st == sprint.Waiting {
		t.Fatalf("b is waiting a tick after its resolve lost to other writers, with its need landed")
	}
	h.clean("resolved")
}

func TestATickThatWentStaleLeavesAFullReadDue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	_, hb, _ := h.st.Machine(h.ctx)
	if hb.Full.IsZero() {
		t.Fatalf("a finished tick left a full read due")
	}
	// A part that lost every attempt: the next tick reads the whole sprint.
	h.tick(time.Second)
	h.work("m1")
	h.st.B = &loseTickPart{Mem: h.m, verb: "tick ask", left: FenceTries}
	if _, err := h.st.Tick(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.st.B = h.m
	if _, hb, _ = h.st.Machine(h.ctx); !hb.Full.IsZero() {
		t.Fatalf("a tick whose part lost every attempt left no full read due: %+v", hb)
	}
	h.tick(time.Second)
	if res := h.machine(); res.Idle || len(h.snap().Readers.Of("s1-1")) != 2 {
		t.Fatalf("the next tick: idle=%v, s1-1 asked of %d", res.Idle, len(h.snap().Readers.Of("s1-1")))
	}
}

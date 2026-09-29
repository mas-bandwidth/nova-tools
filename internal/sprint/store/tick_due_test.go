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

// stopAtFirstPart stops the machine as the tick's first part takes the fence,
// as a stop run beside the tick would.
type stopAtFirstPart struct {
	*Mem
	h    *harness
	part string
}

func (s *stopAtFirstPart) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if s.part == "" && strings.HasPrefix(op.Verb, "tick ") {
		s.part = strings.TrimPrefix(op.Verb, "tick ")
		other := &Store{B: s.Mem, Names: s.h.st.Names, Actor: "coordinator", Now: s.h.st.Now, NewID: s.h.st.NewID}
		if _, _, _, err := other.SetMachine(ctx, false); err != nil {
			return false, err
		}
	}
	return s.Mem.Acquire(ctx, gen, op)
}

func TestNoPartBeginsAfterStop(t *testing.T) {
	t.Parallel()
	h := raceScene(t)
	h.startMachine()
	x := &stopAtFirstPart{Mem: h.m, h: h}
	h.st.B = x
	res := h.machine()
	h.st.B = h.m
	if x.part == "" {
		t.Fatalf("the tick ran no part")
	}
	for _, p := range res.Parts {
		if p.Name != x.part {
			t.Errorf("the part %s ran after stop returned during %s", p.Name, x.part)
		}
	}
	if res.State != Stopped || !strings.Contains(res.Halted, "did not begin") {
		t.Fatalf("the tick does not say it halted: %+v", res)
	}
	if len(res.Parts) != 1 || len(res.Parts[0].Moved) == 0 {
		t.Fatalf("the part in flight did not finish: %+v", res.Parts)
	}
	h.clean("halted")
	// Started again, the tick does what was left.
	h.startMachine()
	h.tick(time.Second)
	h.machine()
	s := h.snap()
	if len(s.Readers.Of("rv")) != 2 || s.StreamCtl("s3").F("state") == sprint.StreamStopped {
		t.Fatalf("after start: rv asked of %d, s3 %s", len(s.Readers.Of("rv")), s.StreamCtl("s3").F("state"))
	}
}

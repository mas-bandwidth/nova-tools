package store

// The audit's gaps of the trigger rule (every card is final, held by an
// outside actor, moved by a trigger, or the subject of an open judgment),
// each inverted: the sequence that left a card silent now moves it or names
// it in the inbox, read as a coordinator reads it (cursor advanced).

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// readInbox is the coordinator reading the inbox and moving the cursor past it.
func (h *harness) readInbox() {
	h.t.Helper()
	v, err := h.st.Inbox(h.ctx, 10*time.Minute, 30*time.Minute, 10000)
	if err != nil {
		h.t.Fatal(err)
	}
	if v.Last != "" {
		if err := h.m.SetCursor(h.ctx, v.Last); err != nil {
			h.t.Fatal(err)
		}
	}
}

// judgmentsOn is the types of the open judgments on the primary.
func (h *harness) judgmentsOn(id string) []string {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, o := range open {
		if o.Subject() == id {
			out = append(out, o.Note.Type)
		}
	}
	return out
}

// The landing merge step resolves what waits on the card it lands.
func TestTriggerLandingResolvesWaiters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s2-1"}, Needs: []string{"s1-1"}}))
	h.through("s1-1")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	h.readInbox()
	if h.state("s1-1") != sprint.Landed || h.state("s2-1") != sprint.Ready {
		t.Fatalf("s1-1 %s, s2-1 %s", h.state("s1-1"), h.state("s2-1"))
	}
	h.clean("resolved by the landing")
}

// A mutated step that moves a primary waiting -> ready past a need that has
// not landed is refused by the engine's lifecycle check, whether it wraps a
// real step's plan (which carries the pre-state) or builds its own.
func TestTheEngineRefusesAMovePastANeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s2-1"}, Needs: []string{"s1-1"}}))
	move := func(s *sprint.Snapshot) sprint.Unit {
		c := s.Work.Card("s2-1")
		return sprint.Unit{Key: c.ID, Stream: c.Row, Changes: []sprint.Change{{Table: sprint.Work, Entry: ntable.BatchMemberEntry{ID: c.ID,
			Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}},
			Move:   &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Ready}}}}}
	}
	own := Step{Verb: "mutant", Load: []string{sprint.Work}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.Plan{Units: []sprint.Unit{move(s)}}
	}}
	wrapped := Step{Verb: "mutant", Load: []string{sprint.Work}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.Resolve(s, sprint.ResolveReq{})
		p.Units = append(p.Units, move(s))
		return p
	}}
	for _, step := range []Step{own, wrapped} {
		res := h.run(step)
		if len(res.Refused) != 1 || h.state("s2-1") != sprint.Waiting {
			t.Fatalf("a move past a need: %+v; s2-1 is %s", res, h.state("s2-1"))
		}
	}
	h.clean("still waiting")
}

// A mutated step that lands a sentinel is refused by the engine: only
// release lands one.
func TestTheEngineRefusesASentinelLandedByAnotherStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	mutant := Step{Verb: "mutant", Load: []string{sprint.Work}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.SentinelsDue(s, "")
		c := s.Work.Card("stop")
		p.Units = append(p.Units, sprint.Unit{Key: c.ID, Stream: c.Row, Changes: []sprint.Change{{Table: sprint.Work, Entry: ntable.BatchMemberEntry{ID: c.ID,
			Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}},
			Move:   &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Landed}}}}})
		return p
	}}
	res := h.run(mutant)
	if len(res.Refused) == 0 || h.state("stop") != sprint.Waiting {
		t.Fatalf("a sentinel landed by another step: %+v; stop is %s", res, h.state("stop"))
	}
	h.clean("still waiting")
}

// A returned primary is back in review with a judgment open on it, whatever
// the cursor: rework, accept (its reads stand at its head), or drop.
func TestTriggerReturnedPrimaryIsAJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.through("s1-1")
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "suspect"}))
	h.readInbox()
	if got := h.judgmentsOn("s1-1"); h.state("s1-1") != sprint.Review || len(got) != 1 || got[0] != sprint.NReturned {
		t.Fatalf("s1-1 is %s with %v open", h.state("s1-1"), got)
	}
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	if got := h.judgmentsOn("s1-1"); len(got) != 0 {
		t.Fatalf("accept left %v open", got)
	}
	h.clean("accepted again")
}

package store

// A step writes a table property in the same batch as its members, guarded on
// the value its plan read (L1 contract amendment, table properties, section 4).

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// dealWithProp is the deal verb whose plan also writes fleet.deal_index, from
// the value it read to next(read).
func dealWithProp(r sprint.DealReq, next func(was string, ok bool) string, calls *int) Step {
	s := DealStep(r)
	plan := s.Plan
	s.Plan = func(snap *sprint.Snapshot) sprint.Plan {
		*calls++
		p := plan(snap)
		was, ok := snap.Fleet.Prop("deal_index")
		p.Props = append(p.Props, sprint.PropWrite{Table: sprint.Fleet, Name: "deal_index", Value: next(was, ok), Was: was, WasAbsent: !ok})
		return p
	}
	return s
}

func propOf(t *testing.T, h *harness, name string) (string, bool) {
	t.Helper()
	return h.snap().Fleet.Prop(name)
}

func TestAStepWritesATablePropertyWithItsMembers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	calls := 0
	h.must(dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}, func(string, bool) string { return "m1" }, &calls))
	if v, ok := propOf(t, h, "deal_index"); !ok || v != "m1" || h.state("s1-1") != sprint.Working {
		t.Fatalf("after the deal: deal_index %q %v, s1-1 %s", v, ok, h.state("s1-1"))
	}
	// the next deal reads it and moves it on
	h.must(dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}, func(was string, _ bool) string { return was + "+" }, &calls))
	if v, _ := propOf(t, h, "deal_index"); v != "m1+" {
		t.Fatalf("deal_index %q, want m1+", v)
	}
}

// A property another writer moved between the plan's read and its write
// refuses the step's first manifest (PROPGUARD): nothing of it applies, and
// the step is planned again on a fresh read.
func TestAMovedPropertyRefusesTheStepAndItIsPlannedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	set := func(v string) {
		s := h.snap()
		m := ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
			OperationID: "other-" + v, Members: []ntable.BatchMemberEntry{}, Props: map[string]string{"deal_index": v}}
		if _, err := h.m.Apply(h.ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	set("5")
	calls := 0
	h.must(dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}, func(was string, _ bool) string {
		if was == "5" {
			set("7") // another writer, after this plan read 5
		}
		return was + "+1"
	}, &calls))
	if v, _ := propOf(t, h, "deal_index"); v != "7+1" || calls != 2 {
		t.Fatalf("deal_index %q after %d plans, want 7+1 after 2", v, calls)
	}
}

// The store's abandon-and-repair with the property in the first manifest: a
// deal whose first manifest never applies is abandoned whole, its property
// with it.
func TestAnAbandonedDealLeavesItsPropertyUnwritten(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.m.Fail = func(p string) error {
		if p == "apply t-fleet before" {
			return errors.New("down")
		}
		return nil
	}
	calls := 0
	if _, err := h.st.Run(h.ctx, dealWithProp(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}, func(string, bool) string { return "m1" }, &calls)); !errors.Is(err, ErrUnknown) {
		t.Fatalf("start: %v", err)
	}
	h.m.Fail = nil
	s := h.snap()
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: "s1-1.w1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "m1", Col: sprint.DoneOK, Score: 1}}}}); err != nil {
		t.Fatal(err)
	}
	h.tick(2 * time.Minute)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	if h.m.Pending() != nil || len(res.Repaired) != 1 || !strings.Contains(res.Repaired[0], "abandoned") {
		t.Fatalf("past the grace: %+v", res)
	}
	if v, ok := propOf(t, h, "deal_index"); ok {
		t.Fatalf("an abandoned deal wrote deal_index %q", v)
	}
}

// A first manifest finished entry by entry past the grace, one of whose cards
// applies: its property applies after it.
func TestARepairedDealWritesItsPropertyWhenACardApplied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.m.Fail = func(p string) error {
		if p == "apply t-fleet before" {
			return errors.New("down")
		}
		return nil
	}
	calls := 0
	if _, err := h.st.Run(h.ctx, dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}, func(string, bool) string { return "m2" }, &calls)); !errors.Is(err, ErrUnknown) {
		t.Fatalf("start: %v", err)
	}
	h.m.Fail = nil
	s := h.snap()
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: "s1-1.w1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "m1", Col: sprint.DoneOK, Score: 1}}}}); err != nil {
		t.Fatal(err)
	}
	h.tick(2 * time.Minute)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	if h.m.Pending() != nil || len(res.Repaired) != 1 || strings.Contains(res.Repaired[0], "abandoned") {
		t.Fatalf("past the grace: %+v", res)
	}
	if v, ok := propOf(t, h, "deal_index"); !ok || v != "m2" {
		t.Fatalf("deal_index %q %v, want m2: a card of the deal applied", v, ok)
	}
}

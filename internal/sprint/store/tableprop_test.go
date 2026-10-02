package store

// A step writes a table property in the same batch as its members, guarded on
// the value its plan read (docs/SPEC-NOVA-TABLE.md, table properties).

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// dealWithProp is the deal verb whose plan also writes fleet.probe_index, from
// the value it read to next(read).
func dealWithProp(r sprint.DealReq, next func(was string, ok bool) string, calls *int) Step {
	s := DealStep(r)
	plan := s.Plan
	s.Plan = func(snap *sprint.Snapshot) sprint.Plan {
		*calls++
		p := plan(snap)
		was, ok := snap.Fleet.Prop("probe_index")
		p.Props = append(p.Props, sprint.PropWrite{Table: sprint.Fleet, Name: "probe_index", Value: next(was, ok), Was: was, WasAbsent: !ok})
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
	if v, ok := propOf(t, h, "probe_index"); !ok || v != "m1" || h.state("s1-1") != sprint.Working {
		require.Failf(t, "", "after the deal: deal_index %q %v, s1-1 %s", v, ok, h.state("s1-1"))
	}
	// the next deal reads it and moves it on
	h.must(dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}, func(was string, _ bool) string { return was + "+" }, &calls))
	v, _ := propOf(t, h, "probe_index")
	require.Equal(t, "m1+", v, "deal_index %q, want m1+", v)
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
			OperationID: "other-" + v, Members: []ntable.BatchMemberEntry{}, Props: map[string]string{"probe_index": v}}
		_, err := h.m.Apply(h.ctx, m)
		require.NoError(t, err)
	}
	set("5")
	calls := 0
	h.must(dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}, func(was string, _ bool) string {
		if was == "5" {
			set("7") // another writer, after this plan read 5
		}
		return was + "+1"
	}, &calls))
	v, _ := propOf(t, h, "probe_index")
	require.Equal(t, "7+1", v, "deal_index %q after %d plans, want 7+1 after 2", v, calls)
	require.Equal(t, 2, calls, "deal_index %q after %d plans, want 7+1 after 2", v, calls)
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
	_, err := h.st.Run(h.ctx, dealWithProp(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}, func(string, bool) string { return "m1" }, &calls))
	require.ErrorIs(t, err, ErrUnknown, "start: %v", err)
	h.m.Fail = nil
	s := h.snap()
	_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: "s1-1.w1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "m1", Col: sprint.DoneOK, Score: 1}}}})
	require.NoError(t, err)
	h.tick(2 * time.Minute)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	require.Nil(t, h.m.Pending(), "past the grace: %+v", res)
	require.Len(t, res.Repaired, 1, "past the grace: %+v", res)
	require.Contains(t, res.Repaired[0], "abandoned", "past the grace: %+v", res)
	v, ok := propOf(t, h, "probe_index")
	require.False(t, ok, "an abandoned deal wrote deal_index %q", v)
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
	_, err := h.st.Run(h.ctx, dealWithProp(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}, func(string, bool) string { return "m2" }, &calls))
	require.ErrorIs(t, err, ErrUnknown, "start: %v", err)
	h.m.Fail = nil
	s := h.snap()
	_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: "s1-1.w1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "m1", Col: sprint.DoneOK, Score: 1}}}})
	require.NoError(t, err)
	h.tick(2 * time.Minute)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	require.Nil(t, h.m.Pending(), "past the grace: %+v", res)
	require.Len(t, res.Repaired, 1, "past the grace: %+v", res)
	require.NotContains(t, res.Repaired[0], "abandoned", "past the grace: %+v", res)
	v, ok := propOf(t, h, "probe_index")
	require.True(t, ok, "deal_index %q %v, want m2: a card of the deal applied", v, ok)
	require.Equal(t, "m2", v, "deal_index %q %v, want m2: a card of the deal applied", v, ok)
}

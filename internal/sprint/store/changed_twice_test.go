package store

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// Drop of a needed card is refused unless Cascade is true.
func TestDropRefusesNeededCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p3", "p4"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p5"}, Needs: []string{"p3", "p4"}}))
	// dropping p3 without cascade should be refused
	res := h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p3"}}, Reason: "why"}))
	require.Len(t, res.Refused, 1, "drop p3 should be refused: %+v", res)
	require.Contains(t, res.Refused[0].Why, "p3 is needed by p5", "refusal should name dependant")
	require.Contains(t, res.Refused[0].Why, "--cascade", "refusal should suggest cascade")
	// dropping with cascade should succeed and drop p5 too
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p3"}}, Reason: "why", Cascade: true}))
	require.Equal(t, "", h.state("p3"), "p3 should be off the table")
	require.Equal(t, "", h.state("p5"), "p5 should be off the table")
	// p4 should still be ready
	require.Equal(t, sprint.Ready, h.state("p4"), "p4 should be ready")
	h.clean("dropped with cascade")
}

// A step whose plan changes one card twice: agreeing changes are one entry;
// disagreeing ones refuse the step whole, naming both causes, and write
// nothing.
func TestTwoChangesOfOneCardInOneStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	two := func(a, b map[string]string) Step {
		return Step{Verb: "twice", Load: All, Plan: func(s *sprint.Snapshot) sprint.Plan {
			c := s.Work.Card("s1-1")
			at := func(set map[string]string) sprint.Change {
				return sprint.Change{Table: sprint.Work, Entry: setOf(c, set)}
			}
			return sprint.Plan{Units: []sprint.Unit{
				{Key: "first", Moved: "the first", Changes: []sprint.Change{at(a)}},
				{Key: "second", Moved: "the second", Changes: []sprint.Change{at(b)}},
			}}
		}}
	}
	h.must(two(map[string]string{"x": "1"}, map[string]string{"y": "2"}))
	c := h.snap().Work.Card("s1-1")
	require.Equal(t, "1", c.F("x"), "the agreeing changes: %v", c.Fields)
	require.Equal(t, "2", c.F("y"), "the agreeing changes: %v", c.Fields)
	before := h.revisions()
	res := h.run(two(map[string]string{"x": "3"}, map[string]string{"x": "4"}))
	require.Len(t, res.Refused, 2, "the disagreeing changes: %+v", res)
	require.Contains(t, res.Refused[0].Why, "by first (the first) and by second (the second)", "the disagreeing changes: %+v", res)
	require.Contains(t, res.Refused[0].Why, "they set x to 3 and to 4", "the disagreeing changes: %+v", res)
	h.nothingWritten(before)
}

// setOf changes fields of a card where it is, guarded at its revision.
func setOf(c *sprint.Card, set map[string]string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Set: set,
		Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}}
}

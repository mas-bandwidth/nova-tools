package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The property test's finding of seed 7, its shortest sequence as a test; and
// the step builder's rule for two changes of one card in one step.

// Seed 7: the printed ack of a group of two blocked judgments on one primary
// (it needs two cards, both dropped) failed: the ack changed the primary
// twice in one step. It waives both needs at once, and the primary is ready.
func TestAckOfTwoBlockedJudgmentsOnOnePrimaryWaivesBoth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p3", "p4"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p5"}, Needs: []string{"p3", "p4"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p3"}}, Reason: "why"}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p4"}}, Reason: "why"}))
	var notes []string
	for _, o := range h.openOf(sprint.NBlocked) {
		notes = append(notes, o.Note.ID)
	}
	require.Len(t, notes, 2, "two blocked judgments: %v", notes)
	var ack string
	for _, c := range h.commandsOf(sprint.NBlocked) {
		if c.Decision == "ack" {
			ack = c.Lines[0]
		}
	}
	require.Contains(t, ack, notes[0], "the printed ack does not name both: %q", ack)
	require.Contains(t, ack, notes[1], "the printed ack does not name both: %q", ack)
	h.must(AckStep(sprint.AckReq{Notes: notes, Reason: "none"}))
	got := h.state("p5")
	require.Equal(t, sprint.Ready, got, "p5 is %s", got)
	if w := h.snap().Work.Card("p5").F("waived"); w != "p3,p4" && w != "p4,p3" {
		t.Fatalf("waived %q", w)
	}
	h.clean("acked")
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
	if c := h.snap().Work.Card("s1-1"); c.F("x") != "1" || c.F("y") != "2" {
		t.Fatalf("the agreeing changes: %v", c.Fields)
	}
	before := h.revisions()
	res := h.run(two(map[string]string{"x": "3"}, map[string]string{"x": "4"}))
	if len(res.Refused) != 2 || !strings.Contains(res.Refused[0].Why, "by first (the first) and by second (the second)") ||
		!strings.Contains(res.Refused[0].Why, "they set x to 3 and to 4") {
		t.Fatalf("the disagreeing changes: %+v", res)
	}
	h.nothingWritten(before)
}

// setOf changes fields of a card where it is, guarded at its revision.
func setOf(c *sprint.Card, set map[string]string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Set: set,
		Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}}
}

package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The line the store writes for the ack of a judgment. The ack step's plan
// holds a decided note that names no subject; the store replaces it with one
// that names the subjects the step closes, so the line the log holds says
// which judgment closed and on what.
func TestTheDecidedLineTheStoreWritesForAnAckNamesItsSubjects(t *testing.T) {
	t.Parallel()
	// Test only NMissingNeed; NBlocked (dropped) is no longer reachable via drop
	// without cascade, and with cascade the dependant is also dropped.
	h := newHarness(t)
	h.setup(2)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1", "s1-2"}}))
	seedMissingNeeds(h, "waiter", "first.bad")
	h.must(ResolveStep(sprint.ResolveReq{}))
	open := h.nOpenOf(sprint.NMissingNeed, "waiter")
	require.Len(t, open, 1, "NMissingNeed: %d open judgments on the waiter", len(open))
	before := len(h.lines())
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "unneeded", Who: "tester"}))
	var closing []*sprint.Note
	for _, l := range h.lines()[before:] {
		if l.Note != nil && (l.Kind == sprint.Decided || l.Kind == sprint.Acknowledged) {
			closing = append(closing, l.Note)
		}
	}
	require.Len(t, closing, 1, "NMissingNeed: the ack closed with %+v", closing)
	require.Equal(t, sprint.NMissingNeed, closing[0].Type)
	require.Equal(t, []string{"waiter"}, closing[0].Subjects())
}

// Drop of a needed card is refused unless Cascade is true.
func TestDropRefusesNeededCardInAckLineTest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1", "s1-2"}}))
	// dropping s1-1 without cascade should be refused
	res := h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	require.Len(t, res.Refused, 1, "drop should be refused: %+v", res)
	require.Contains(t, res.Refused[0].Why, "s1-1 is needed by waiter", "refusal should name dependant")
	require.Contains(t, res.Refused[0].Why, "--cascade", "refusal should suggest cascade")
	// dropping with cascade drops waiter too
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "obsolete", Cascade: true}))
	require.Equal(t, "", h.state("s1-1"), "s1-1 should be off the table")
	require.Equal(t, "", h.state("waiter"), "waiter should be off the table")
	h.clean("dropped with cascade")
}

package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The property test's findings left unfixed, each its shortest sequence as a
// test behind a named skip: take the skip out when the finding is fixed.

// Seed 70 (FINDING-F3, not fixed): the overdue decision act prints an ack for
// every judgment; the ack of a primary's ready to accept closes the one
// judgment that holds it, and by section 6 an ack does not write its own
// judgment again, so the primary sits in review with nothing open. The
// no-stall rule's judgment now raises it at the next tick; the design still
// leaves it silent until then. The same holds for stranded in review, reads
// exhausted, and a reached sentinel.
func TestAnAckOfTheOneJudgmentThatHoldsAPrimaryLeavesItSilent(t *testing.T) {
	t.Parallel()
	t.Skip("FINDING-F3: an ack of the one judgment that holds a primary leaves it with nothing open; a design decision (refuse that ack, or write the judgment again), not a few lines")
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p1"}}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"p1"}}}))
	h.takeAndFinish(false, "p1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"p1"}}}))
	h.readAll()
	open := h.openOf(sprint.NReadyToAccept)
	if len(open) != 1 {
		t.Fatalf("ready to accept: %v", open)
	}
	h.tick(26 * time.Minute)
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "none"}))
	h.clean("the ack of ready to accept") // rule 12: p1 review: acceptable, and no judgment is open on it
}

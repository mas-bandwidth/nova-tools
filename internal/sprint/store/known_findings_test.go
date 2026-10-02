package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The property test's findings left unfixed, each its shortest sequence as a
// test behind a named skip: take the skip out when the finding is fixed.

// Seed 70 (FINDING-F3, decided): an ack of the one judgment that holds a
// primary is refused, naming the card's decisions: ack answers only a
// judgment whose decisions list it, and ready to accept, stranded in review,
// reads exhausted and sentinel reached do not; the overdue decision act
// prints only wait for them.
func TestAnAckOfTheOneJudgmentThatHoldsAPrimaryLeavesItSilent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p1"}}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"p1"}}}))
	h.takeAndFinish(false, "p1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"p1"}}}))
	h.readAll()
	open := h.openOf(sprint.NReadyToAccept)
	require.Len(t, open, 1, "ready to accept: %v", open)
	h.tick(26 * time.Minute)
	res := h.run(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "none"}))
	require.Len(t, res.Refused, 1, "the ack of ready to accept: %+v", res)
	require.Empty(t, res.Moved, "the ack of ready to accept: %+v", res)
	require.Len(t, h.openOf(sprint.NReadyToAccept), 1, "ready to accept closed by a refused ack")
	h.clean("the ack of ready to accept refused") // rule 12 holds: the judgment still names p1
}

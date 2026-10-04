package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The machine's accept as a tick part (steps_tick.go, TickAccept): a primary
// in review with the ok reads it needs at its head moves to merging and into
// its stream's merge queue, the coordinator is told once per stream the tick
// queued cards in, and a primary a hold names (its CI red at its head, or
// returned to review at its attempt) is left for the coordinator.
// docs/SPEC-SPRINT.md's accept row, tla/DirtyTick.tla Held and AcceptHolds.

// Accept is mechanical: the primary in review with the ok reads it needs at
// its head moves to merging and into its stream's queue, and the coordinator
// is told once, "ready to merge", with the command to run; the tick records
// the note as the machine, addressed to the coordinator.
func TestStepsTickCoverAcceptsAReviewPrimaryAtItsOkReads(t *testing.T) {
	t.Parallel()
	w := reviewedOK(t)
	p, _ := TickAccept(w.s, TickReq{})
	w.must(p)
	require.Len(t, p.Units, 1, "the one acceptable primary: %+v", p)
	assert.Equal(t, "s1-1", p.Units[0].Key)
	assert.Equal(t, Merging, w.state("s1-1"), "review -> merging on the ok reads it needs")
	require.NotNil(t, w.s.Merge.Placed("s1-1"), "the accepted primary is in its stream's merge queue")
	assert.Equal(t, Queued, w.s.Merge.Placed("s1-1").Col, "the accepted primary is in its stream's merge queue")
	notes := w.notesOf(NReadyToMerge)
	require.Len(t, notes, 1, "one note per stream the tick queued cards in: %+v", notes)
	n := notes[0]
	assert.Equal(t, Happened, n.Kind, "no decision: the merge is the coordinator's")
	assert.Equal(t, MachineActor, n.Who, "the tick records its own note as the machine")
	assert.Equal(t, w.s.Coordinator, n.To, "the note is addressed to the coordinator")
	assert.Equal(t, "s1", n.Stream)
	assert.Equal(t, []string{"s1-1"}, n.Primaries)
	assert.Contains(t, n.What, "1 accepted and queued to merge: s1-1")
	assert.Contains(t, n.What, "run: nova-sprint land --stream s1")
	w.clean("after the accept")
}

// A hold is the coordinator's, never the tick's: a primary whose CI is red at
// its head, and one the coordinator returned to review at its current
// attempt, are left in review with an empty plan and no note (AcceptHeld's
// two holds, R9).
func TestStepsTickCoverLeavesAHeldPrimaryInReview(t *testing.T) {
	t.Parallel()
	for name, hold := range map[string]func(w *world){
		"its CI is red at its head": func(w *world) {
			w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1"}))
		},
		"returned to review at its attempt": func(w *world) {
			first, _ := TickAccept(w.s, TickReq{})
			w.must(first)
			w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "the stream branch went red"}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := reviewedOK(t)
			hold(w)
			p, _ := TickAccept(w.s, TickReq{})
			assert.True(t, p.Empty(), "the held primary is not the tick's to accept: %+v", p)
			assert.Equal(t, Review, w.state("s1-1"), "a held primary stays in review")
			assert.NotEmpty(t, AcceptHeld(w.s.Work.Card("s1-1")), "the hold is named for the coordinator")
		})
	}
}

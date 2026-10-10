package store

// The redo restore through the twin (docs/SPEC-SPRINT.md section 11, redo):
// the CLI shares one twin for every verb (cmd/nova-sprint/main.go), and a
// dropped primary is kept off the table, so the redo step's read must bring
// every work record, placed or kept, through the twin (Step.EveryRecord,
// twin.go, showExtras).

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestRedoRestoresADroppedStreamThroughTheTwin pins the store path of the redo
// restore: a store that shares one twin for every verb, as the CLI does, finds
// a dropped primary by its stream and restores it. Without the every-record
// read on the twin path the dropped cards are absent, so the redo refuses the
// stream as `not in a conflict`.
func TestRedoRestoresADroppedStreamThroughTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.st.ShareTwin(NewTwin()) // the CLI's one twin, for every verb

	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{Stream: "s1"}, Reason: "dropped by mistake", Cascade: true}))
	require.Equal(t, "", h.state("s1-1"), "dropped: s1-1 off the table")

	res := h.must(RedoStep(sprint.RedoReq{Sel: sprint.Sel{Stream: "s1"}, Reason: "re-admitted", Who: "coordinator"}))
	require.Len(t, res.Moved, 3, "one MOVED line per restored card")
	require.Equal(t, sprint.Waiting, h.state("s1-1"))
	require.Equal(t, sprint.Waiting, h.state("s1-2"))
	require.Equal(t, sprint.Waiting, h.state("s1-3"))
	h.clean("restored")
}

// TestRedoRestoresADroppedCardByIDThroughTheTwin is the named-id half of the
// same store path: the dropped card is named, not selected by stream, and the
// twin's read must still bring its kept record.
func TestRedoRestoresADroppedCardByIDThroughTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.st.ShareTwin(NewTwin())

	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Reason: "dropped by mistake"}))
	require.Equal(t, "", h.state("s1-2"), "dropped: s1-2 off the table")

	res := h.must(RedoStep(sprint.RedoReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Reason: "re-admitted", Who: "coordinator"}))
	require.Len(t, res.Moved, 1, "one MOVED line for the restored card")
	require.Equal(t, sprint.Waiting, h.state("s1-2"))
	h.clean("restored")
}

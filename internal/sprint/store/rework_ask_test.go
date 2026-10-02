package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Reworked work is asked round the readers, the readers of its earlier attempt
// not preferred (the owner, 2026-10-01: "yes on the decision."): with one of
// them holding a read it has begun and another reader idle, the machine's tick
// asks the next two round the readers, and a second tick right after moves
// nothing but the drain (errata 3 amendment 12). Asking the earlier pair again
// put a second read on the busy reader, and the next tick's level moved it.
func TestReworkedWorkIsAskedRoundTheReaders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.a2ToReview("s1-1", false)
	h.a2ToReview("s1-2", false)
	ask := func(id string) []*sprint.Card {
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		return readsAt(h.snap(), h.snap().Work.Card(id))
	}
	// attempt 1 of s1-1 is read by reader-a and reader-b: one ok, one broken, reworked
	first := ask("s1-1")
	require.Len(t, first, 2)
	h.must(ReadStep(sprint.ReadReq{As: first[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{first[0].ID}}}))
	h.must(ReadStep(sprint.ReadReq{As: first[1].Row, Verdict: "broken", Finding: "f", Sel: sprint.Sel{IDs: []string{first[1].ID}}}))
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	pair := []string{first[0].Row, first[1].Row}
	require.ElementsMatch(t, []string{"reader-a", "reader-b"}, pair)
	// s1-2 is asked of reader-c and reader-a: reader-a begins its read and holds it,
	// reader-c reads ok and is idle
	for _, rc := range ask("s1-2") {
		if rc.Row == "reader-a" {
			h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
		} else {
			h.must(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
		}
	}
	h.finishAttempt("s1-1", false, "h2")
	h.startMachine()
	h.machine()
	var who []string
	for _, rc := range readsAt(h.snap(), h.snap().Work.Card("s1-1")) {
		who = append(who, rc.Row)
	}
	assert.ElementsMatch(t, []string{"reader-b", "reader-c"}, who, "attempt 2 asked of the next two round the readers")
	res := h.machine()
	for _, p := range res.Parts {
		if p.Name != sprint.PartDrain {
			assert.Empty(t, p.Moved, "a second tick right after moved something in %s", p.Name)
			assert.Zero(t, p.Notes, "a second tick right after wrote notes in %s", p.Name)
		}
	}
	h.clean("asked round the readers")
}

package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A development receipt pins the entire exact candidate. A queued primary
// returned to review makes that candidate stale: no card, control change or
// started-merging note may survive the refusal.
func TestMergeGreenRefusesTheWholeCandidateWhenTheFirstCardIsStale(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	state := w.s.StreamCtl("s1").F("state")
	w.s.Work.Card("s1-1").Col = Review
	w.s.Work.cells, w.s.Work.byPrimary = nil, nil

	p := devMergeFixture(w.s, MergeReq{Stream: "s1", Batch: 10})
	require.Len(t, p.Refused, 1)
	require.Contains(t, p.Refused[0].Why, "current merging head and attempt of s1-1")
	require.Empty(t, p.Units, "a stale candidate moved another pinned card")
	require.Empty(t, p.Notes, "a stale candidate changed the stream's judgments")
	require.Equal(t, state, w.s.StreamCtl("s1").F("state"))
	require.Equal(t, Merging, w.state("s1-2"))
	require.Equal(t, Queued, w.s.Merge.Card("s1-2").Col)
}

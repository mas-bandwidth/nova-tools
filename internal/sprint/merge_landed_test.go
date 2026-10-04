package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The race merge --batch could not survive (docs/SPEC-SPRINT.md section 8, merge --landed):
// two cards queued, the lander pushes the first, the first is returned and queued again
// behind the second before the record. The record names the pushed card and its head, so it
// lands that card and no other; a record naming a head the base does not hold, or a head the
// card no longer has, is refused whole and writes nothing.
func TestMergeRecordsLandOnlyTheCardWhoseHeadWasPushed(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.s.Work.Card("s1-1").Fields["head"] = "h1"
	w.s.Work.Card("s1-2").Fields["head"] = "h2"
	accepted(w, "s1-1", "s1-2")
	// the lander pushed s1-1 at h1; then s1-1 came back and queued again behind s1-2
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "suspect"}))
	w.s.Work.Card("s1-1").Score = w.s.Work.Card("s1-2").Score + 1
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.s.Work.Card("s1-1").Fields["head"] = "h1" // the same head again: the rework did not change it
	queue := w.s.Merge.Cell("s1", Queued)
	require.Equal(t, []string{"s1-2", "s1-1"}, []string{queue[0].ID, queue[1].ID}, "the first card is behind the second")

	positional := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1})
	require.Equal(t, "s1-2", positional.Units[0].Key, "the position form records the card at the front, which was not pushed")

	for name, r := range map[string]MergeReq{
		"the second card, whose head the base does not hold": {Stream: "s1", Landed: []LandedPin{{ID: "s1-2", Head: "h2"}}},
		"the first card at a head it no longer has":          {Stream: "s1", Landed: []LandedPin{{ID: "s1-1", Head: "old", InBase: true}}},
		"a card of another stream":                           {Stream: "s1", Landed: []LandedPin{{ID: "s9-1", Head: "h", InBase: true}}},
		"the pushed card named twice":                        {Stream: "s1", Landed: []LandedPin{{ID: "s1-1", Head: "h1", InBase: true}, {ID: "s1-1", Head: "h1", InBase: true}}},
		"a good card beside a bad one":                       {Stream: "s1", Landed: []LandedPin{{ID: "s1-1", Head: "h1", InBase: true}, {ID: "s1-2", Head: "h2"}}},
	} {
		p := MergeStep(w.s, r)
		require.NotEmpty(t, p.Refused, "%s is refused", name)
		require.Empty(t, p.Units, "%s: nothing is written", name)
		require.NotEmpty(t, p.Refused[0].Key, "%s: the refusal names the card", name)
	}

	p := w.must(MergeStep(w.s, MergeReq{Stream: "s1", Landed: []LandedPin{{ID: "s1-1", Head: "h1", InBase: true}}}))
	require.Equal(t, Landed, w.state("s1-1"), "the pushed card landed")
	require.Equal(t, Merging, w.state("s1-2"), "the card at the front, never pushed, did not land")
	require.Equal(t, []string{"s1-1 merging -> landed"}, []string{p.Units[0].Moved})
}

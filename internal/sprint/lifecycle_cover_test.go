package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// Unit coverage for Rejudge (internal/sprint/lifecycle.go): a repair applies
// its entries one by one to the lifecycle, each judged alone against a fresh
// read, so a landing that is left out satisfies no need. All pure: hand-made
// tables, with no store, clock, sleep, subprocess, network or server.

// lifecycleCoverPre is a fresh read of one stream's row: a waiting primary
// that needs p5, a ready one, a working one, one in review, one in merging,
// and a sentinel waiting in front of them all.
func lifecycleCoverPre() *Snapshot {
	s := &Snapshot{Epoch: 3, Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1"})
	s.Work.Put(&Card{ID: "p1", Row: "s1", Col: Waiting, Score: -1, Rev: 1, Fields: map[string]string{"needs": "p5"}})
	s.Work.Put(&Card{ID: "p2", Row: "s1", Col: Ready, Score: 2, Rev: 2})
	s.Work.Put(&Card{ID: "p3", Row: "s1", Col: Working, Score: 3, Rev: 3})
	s.Work.Put(&Card{ID: "p4", Row: "s1", Col: Review, Score: 4, Rev: 4})
	s.Work.Put(&Card{ID: "p5", Row: "s1", Col: Merging, Score: 5, Rev: 5})
	s.Work.Put(&Card{ID: "sn", Row: "s1", Col: Waiting, Score: 0, Rev: 6, Fields: map[string]string{"kind": "sentinel"}})
	return s
}

// lifecycleCoverMove is a work-table move of a placed card, guarded at the
// place its expectation names: the shape a repair's batch carries.
func lifecycleCoverMove(id, from, to string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: id,
		Expect: &ntable.MemberExpect{Revision: "1", Place: &ntable.PlaceExpect{Row: "s1", Col: from}},
		Move:   &ntable.MemberMoveOp{Row: "s1", Col: to}}
}

func TestLifecycleCoverRejudgeHoldsEveryEntryToTheLifecycle(t *testing.T) {
	t.Parallel()
	pre := lifecycleCoverPre()
	cases := []struct {
		name  string
		verb  string
		entry ntable.BatchMemberEntry
		want  string // the refusal's why, "" when the entry passes
	}{
		{"a repair moves a primary working -> review by finish", "", lifecycleCoverMove("p3", Working, Review), ""},
		{"a repair moves a primary ready -> working by deal", "", lifecycleCoverMove("p2", Ready, Working), ""},
		{"release lands a sentinel from waiting", "release", lifecycleCoverMove("sn", Waiting, Landed), ""},
		{"add moves a primary ready -> waiting in front of the sentinel", "add", lifecycleCoverMove("p2", Ready, Waiting), ""},
		{"a drain's composed move passes over states unjudged", DrainVerb, lifecycleCoverMove("p4", Review, Landed), ""},
		// The refusals, each naming the move it refuses.
		{"a landing from waiting without release refuses", "", lifecycleCoverMove("sn", Waiting, Landed),
			"the lifecycle lands from waiting only a sentinel, and only by release"},
		{"ready -> waiting without add refuses", "release", lifecycleCoverMove("p2", Ready, Waiting),
			"the lifecycle moves ready -> waiting only as the effect of inserting a sentinel"},
		{"a move the lifecycle has none of refuses", "", lifecycleCoverMove("p2", Ready, Merging),
			"the lifecycle has no move ready -> merging"},
		{"a waiting -> ready with an unlanded need refuses", "", lifecycleCoverMove("p1", Waiting, Ready),
			"p1 needs p5, not landed: it waits"},
	}
	for _, c := range cases {
		refused := Rejudge(pre, c.verb, []Change{change(Work, c.entry)})
		if c.want == "" {
			require.Empty(t, refused, c.name)
			continue
		}
		require.Len(t, refused, 1, c.name)
		require.Equal(t, c.entry.ID, refused[0].Key, c.name)
		require.Contains(t, refused[0].Why, c.want, c.name)
	}
}

// Each entry is its own unit: the landing a repair keeps lends to the waiter
// judged with it, and one left out (skipped) satisfies no need.
func TestLifecycleCoverRejudgeSkippedLandingSatisfiesNoNeed(t *testing.T) {
	t.Parallel()
	pre := lifecycleCoverPre()
	land := []Change{change(Work, lifecycleCoverMove("p5", Merging, Landed))}
	wait := []Change{change(Work, lifecycleCoverMove("p1", Waiting, Ready))}
	// The landing the repair keeps lends to the waiter judged with it, in
	// either order; one left out (skipped) does not happen and lends nothing.
	require.Empty(t, Rejudge(pre, "", append(wait, land...)), "the kept landing lends to the waiter")
	require.Empty(t, Rejudge(pre, "", append(land, wait...)), "the kept landing lends to the waiter in either order")
	skipped := Rejudge(pre, "", wait)
	require.Len(t, skipped, 1, "a landing the repair skips satisfies no need")
	require.Equal(t, "p1", skipped[0].Key, "a landing the repair skips satisfies no need")
	require.Contains(t, skipped[0].Why, "p1 needs p5, not landed", "a landing the repair skips satisfies no need")
}

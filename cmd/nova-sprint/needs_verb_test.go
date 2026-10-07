package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// needs prints one stream's waiting cards as a dependency graph: the chain
// order from the roots down (a card after every card it needs; the work order
// within a depth), each card with its unmet needs and their states, the roots
// marked, each card's depth, the width at each depth, and a closing count of
// the cards whose needs name a dropped or absent id (one card once, however
// many of its needs name one). A card with no waiting need is a root at depth
// 0. A need naming a dropped card or no record at all never reaches this
// verb: add refuses it and drop refuses the needed card without --cascade
// (docs/SPEC-SPRINT.md section 11), so the dropped-or-absent count of a
// sprint the verbs built is zero; the words for those states are pinned at
// the store, where a stored record still carries them.
func TestNeedsPrintsRootsDepthAndWidthOfAWaitingChain(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	// The scores put the s2 chain in reverse work order: d sorts first, a
	// last, so only the chain order (a card after every card it needs) can
	// print the chain from its root down.
	ta.ok("add --one --stream s1 --count 1")
	ta.ok("add --one --stream s2 a --needs s1-1 --score 9")
	ta.ok("add --one --stream s2 b --needs a --score 8")
	ta.ok("add --one --stream s2 c --needs b --score 7")
	ta.ok("add --one --stream s2 d --needs c --score 6")
	ta.ok("add --one --stream s1 x")
	ta.ok("add --one --stream s1 y")
	ta.ok("add --one --stream s1 m --needs x,y")

	out := ta.ok("needs --stream s2")
	rootAt := strings.Index(out, "depth=0 ROOT card a column waiting needs need s1-1 column ready")
	bAt := strings.Index(out, "depth=1 card b column waiting needs need a column waiting")
	cAt := strings.Index(out, "depth=2 card c column waiting needs need b column waiting")
	dAt := strings.Index(out, "depth=3 card d column waiting needs need c column waiting")
	require.Greater(t, rootAt, -1, "a is the root at depth 0, on the live need")
	require.Greater(t, bAt, rootAt, "b needs the waiting a, at depth 1, after it in chain order")
	require.Greater(t, cAt, bAt, "c at depth 2, after b in chain order")
	require.Greater(t, dAt, cAt, "d at depth 3, after c in chain order")
	require.Contains(t, out, "depth 0: 1, depth 1: 1, depth 2: 1, depth 3: 1", "the width at each depth")
	require.Contains(t, out, "NEEDS stream=s2 cards=4 dropped-or-absent=0", "no need names a dropped or absent id")

	s1 := ta.ok("needs --stream s1")
	require.Contains(t, s1, "depth=0 ROOT card m column waiting needs need x column ready, need y column ready", "m names both its live needs")
	require.Contains(t, s1, "NEEDS stream=s1 cards=1 dropped-or-absent=0", "m's two live needs count no orphan")

	roots := ta.ok("needs --stream s2 --roots")
	require.Contains(t, roots, "depth=0 ROOT card a", "--roots keeps the roots")
	require.NotContains(t, roots, "depth=1 card b", "--roots drops the cards below the roots")
	require.Contains(t, roots, "depth 0: 1, depth 1: 1, depth 2: 1, depth 3: 1", "--roots keeps the widths")

	var v struct {
		Streams []struct {
			Stream string `json:"stream"`
			Cards  []struct {
				ID    string `json:"id"`
				Depth int    `json:"depth"`
				Root  bool   `json:"root"`
			} `json:"cards"`
		} `json:"streams"`
		Cards   int `json:"cards"`
		Orphans int `json:"dropped_or_absent"`
	}
	ta.json("needs --stream s2", &v)
	require.Len(t, v.Streams, 1, "one stream in the JSON view")
	require.Equal(t, "s2", v.Streams[0].Stream, "the stream the verb names")
	require.Len(t, v.Streams[0].Cards, 4, "every waiting card")
	for i, id := range []string{"a", "b", "c", "d"} {
		require.Equal(t, id, v.Streams[0].Cards[i].ID, "the chain's card %d", i)
		require.Equal(t, i, v.Streams[0].Cards[i].Depth, "%s at depth %d", id, i)
	}
	require.True(t, v.Streams[0].Cards[0].Root, "a is a root")
	require.False(t, v.Streams[0].Cards[1].Root, "b waits on the root, no root itself")
	require.Equal(t, 0, v.Orphans, "s2's dropped-or-absent count: every need is on the table")

	var w struct {
		Streams []struct {
			Stream  string `json:"stream"`
			Orphans int    `json:"dropped_or_absent"`
		} `json:"streams"`
		Cards   int `json:"cards"`
		Orphans int `json:"dropped_or_absent"`
	}
	ta.json("needs", &w)
	require.Len(t, w.Streams, 2, "both streams' waiting cards in the whole view")
	require.Equal(t, 0, w.Streams[1].Orphans, "s1's count: m's two live needs are no orphan")
	require.Equal(t, 5, w.Cards, "every waiting card of the sprint")
	require.Equal(t, 0, w.Orphans, "the sprint's count of the cards on a dropped or absent need: none")
	ta.clean()
}

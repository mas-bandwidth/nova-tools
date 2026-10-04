package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// needs prints one stream's waiting cards as a dependency graph: the chain
// order from the roots down, each card with its unmet needs and their states,
// the roots marked, each card's depth, the width at each depth, and a closing
// count of the cards whose needs name a dropped or absent id. A card whose
// need is off the table (dropped) is a root at depth 0 with no tick run.
func TestNeedsPrintsRootsDepthAndWidthOfAWaitingChain(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 a --needs s1-1")
	ta.ok("add --stream s2 b --needs a")
	ta.ok("add --stream s2 c --needs b")
	ta.ok("add --stream s2 d --needs c")
	ta.ok("drop s1-1 --reason obsolete")

	out := ta.ok("needs --stream s2")
	require.Contains(t, out, "depth=0 ROOT a needs s1-1 dropped", "a is the root at depth 0, on the dropped need")
	require.Contains(t, out, "depth=1 b needs a waiting", "b needs the waiting a, at depth 1")
	require.Contains(t, out, "depth=2 c needs b waiting", "c at depth 2")
	require.Contains(t, out, "depth=3 d needs c waiting", "d at depth 3")
	require.Contains(t, out, "depth 0: 1, depth 1: 1, depth 2: 1, depth 3: 1", "the width at each depth")
	require.Contains(t, out, "NEEDS OK cards=4 dropped-or-absent=1", "the sprint's count of cards on a dropped or absent need")

	roots := ta.ok("needs --stream s2 --roots")
	require.Contains(t, roots, "depth=0 ROOT a", "--roots keeps the roots")
	require.NotContains(t, roots, "depth=1 b", "--roots drops the cards below the roots")
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
	require.Equal(t, 0, v.Streams[0].Cards[0].Depth, "a is at depth 0")
	require.True(t, v.Streams[0].Cards[0].Root, "a is a root")
	require.Equal(t, 1, v.Orphans, "the sprint's dropped-or-absent count")
	ta.clean()
}

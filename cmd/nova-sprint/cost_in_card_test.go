package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The cost is tracked in the card (the owner, 2026-10-01: "The cost needs to be
// tracked IN THE CARD"; internal/sprint/cost.go): a card with a provider-failed take,
// a failed take, a take that finished, two reads and a read returned by a reader then
// removed carries every one of them, its totals are right, `card <id>` prints them
// from the card, and its landing puts the same total in the stream's cost cell, once.
func TestTheCardCarriesEveryConsumerWhoeverIsRemoved(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 1 --brief-file " + proBriefFile(t))
	ta.deal(1)
	use := func(usd string) string {
		return " --usage 'input=10 output=1 actual_usd=" + usd + " actual_by=harness'"
	}

	// attempt 1: the provider fails a take, the card is dealt again, the next take fails
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'provider failure: 529 overloaded'" + use("0.001"))
	ta.deal(1)
	gen := ta.workGen("m1", "s1-1.w1")
	ta.ok("take --as m1 s1-1.w1@" + gen)
	ta.ok("finish --as m1 s1-1.w1@" + gen + " --failed --report 'tests red'" + use("0.002"))
	// attempt 2 finishes
	ta.ok("rework s1-1 --fix 'handle the empty case'")
	ta.ok("take --as m1 s1-1.w2@1")
	ta.ok("finish --as m1 s1-1.w2@1" + use("0.004"))

	// the reads: one handed back by its reader, asked of another, then two ok
	ta.ok("ask")
	var asked []string
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if code, _, _ := ta.do("read --as " + rd + " --begin s1-1.r2." + rd); code == 0 {
			asked = append(asked, rd)
		}
	}
	require.Len(t, asked, 2)
	gone := asked[0]
	ta.ok("read --as " + gone + " --return s1-1.r2." + gone + " --reason 'no verdict'" + use("0.0008"))
	ta.ok("ask")
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if rd != gone {
			ta.ok("read --as " + rd + " --ok s1-1.r2." + rd + use("0.0001"))
		}
	}
	// the reader that returned its read leaves the readers table: its read's cost stays in the card
	ta.ok("reader remove " + gone)

	out := ta.ok("card s1-1")
	lines := costLines(out)
	require.Len(t, lines, 7, out)
	ends := []string{}
	for _, l := range lines[:6] {
		for _, w := range strings.Fields(l) {
			if v, ok := strings.CutPrefix(w, "end="); ok {
				ends = append(ends, v)
			}
		}
	}
	assert.ElementsMatch(t, []string{"provider-failure", "failed", "ok", "returned", "ok", "ok"}, ends)
	assert.Contains(t, out, "who="+gone+" ", "the removed reader's read is in the card")
	// 0.001 + 0.002 + 0.004 + 0.0008 + 0.0001 + 0.0001
	assert.Contains(t, lines[6], "COST TOTAL consumers=6 input=60 ")
	assert.Contains(t, lines[6], "actual_usd=0.008 actual_by=harness actual_of=6/6 charged_usd=0.008")

	var v cardView
	ta.json("card s1-1", &v)
	require.Len(t, v.Cost.Consumers, 6)
	assert.Equal(t, "0.008", v.Cost.Total.Charged)

	// the landing puts the card's total in the stream's cell, and a replay changes nothing
	ta.ok("accept --stream s1")
	ta.ok("merge --stream s1 --op land-s1")
	ta.ok("merge --stream s1 --op land-s1")
	cells := ta.costCells()
	assert.Equal(t, "$0.01", cells["s1"])
	assert.Equal(t, "$0.01", cells[""])
	var landed cardView
	ta.json("card s1-1", &landed)
	assert.Equal(t, "0.008", landed.Primary.F(sprint.FieldCost))
	assert.Equal(t, v.Cost.Total, landed.Cost.Total, "the replay added nothing to the card")
}

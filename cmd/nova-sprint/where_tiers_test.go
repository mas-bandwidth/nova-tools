package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// where's cost visibility (docs/SPEC-SPRINT.md section 1; sprint/cost_view.go): where
// --json --costs carries `tiers` (every card by its brief's tier) at the top and, on each work row,
// `per_landed`, `tiers` and `cost_by_tier`, read from the cost records the cards hold
// (store.TierCosts); a cell is a string as before, and the plain view reads no card for them.
func TestWhereCarriesTiersAndCostByTier(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --stream s2 --count 1 --brief-file " + writeBrief(t, "a flash card, tier: flash"))
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})
	var v whereView
	ta.json("where --costs", &v)
	assert.Equal(t, map[string]int{"pro": 2, "flash": 1}, v.Tiers, "every card by its brief's tier")
	s1 := v.Tables["work"]["s1"]
	assert.Equal(t, "$1.50", s1["per_landed"], "three dollars over two landed cards")
	assert.Equal(t, "$3.00", s1["cost"], "the cost cell as it was")
	assert.Equal(t, map[string]any{"pro": float64(2)}, s1["tiers"])
	assert.Equal(t, map[string]any{"pro": "$3.00"}, s1["cost_by_tier"], "both attempts ran on pro")
	s2 := v.Tables["work"]["s2"]
	assert.Equal(t, "-", s2["per_landed"], "nothing landed")
	assert.Equal(t, map[string]any{"flash": float64(1)}, s2["tiers"])
	assert.NotContains(t, s2, "cost_by_tier", "nothing spent")
	assert.Equal(t, "0", s2["landed"], "a count cell is a string")
	var plain whereView
	ta.json("where", &plain)
	assert.Nil(t, plain.Tiers, "the plain view reads no card for them")
	assert.NotContains(t, plain.Tables["work"]["s1"], "cost_by_tier")
	code, _, stderr := ta.do("where --costs")
	assert.Equal(t, 2, code, "--costs without --json")
	assert.Contains(t, stderr, "--costs is a field of the JSON view")
}

// The header under the where view's title reads a late tick as running, the lateness
// after the progress line; STOPPED and DONE stand alone (store.WhereMachineLine).
func TestWhereHeaderReadsALateTickAsRunning(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "1/2 50.0%", whereHeader("1/2 50.0%", "machine: running"))
	assert.Equal(t, "1/2 50.0% (tick late 9s)", whereHeader("1/2 50.0%", "machine: running (tick late 9s)"))
	assert.Equal(t, "STOPPED", whereHeader("1/2 50.0%", "machine: STOPPED"))
	assert.Equal(t, "DONE", whereHeader("2/2 100.0%", "machine: DONE"))
}

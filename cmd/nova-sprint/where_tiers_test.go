package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// where's cost visibility (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-04): the work
// table ends with `per landed`, the stream's dollars per landed card; where --json carries
// `tiers` (every card by its brief's tier) at the top and, on each work row, `per_landed`,
// `tiers` and `cost_by_tier`, from the tick's where record; the JSON is additive.
func TestWhereCarriesTiersAndPerLandedCost(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --one --stream s2 --count 1 --brief-file " + writeBrief(t, "a flash card, tier: flash"))
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})
	ta.ok("tick") // the record is the tick's
	var v whereView
	ta.json("where", &v)
	assert.Equal(t, map[string]int{"pro": 2, "flash": 1}, v.Tiers, "every card by its brief's tier")
	s1 := v.Tables["work"]["s1"]
	assert.Equal(t, "$1.50", s1["per_landed"], "three dollars over two landed cards")
	assert.Equal(t, "$3.00", s1["cost"], "the cost cell as it was")
	assert.Equal(t, map[string]any{"pro": float64(2)}, s1["tiers"])
	assert.Equal(t, map[string]any{"pro": "$3.00"}, s1["cost_by_tier"], "both attempts ran on pro")
	s2 := v.Tables["work"]["s2"]
	assert.Equal(t, "-", s2["per_landed"], "nothing landed")
	assert.Equal(t, map[string]any{"flash": float64(1)}, s2["tiers"])
	// the text table: per landed is the last column, after cost
	var header string
	for _, l := range strings.Split(ta.ok("where"), "\n") {
		if strings.HasPrefix(l, "work ") {
			header = l
		}
		if strings.HasPrefix(l, "s1 ") {
			cells := strings.Split(l, "|")
			assert.Equal(t, "$1.50", strings.TrimSpace(cells[len(cells)-1]), "s1's per landed: %s", l)
			assert.Equal(t, "$3.00", strings.TrimSpace(cells[len(cells)-2]), "s1's cost: %s", l)
		}
	}
	require.Regexp(t, "\\| +cost \\| per landed$", header, "per landed is the last column, after cost")
}

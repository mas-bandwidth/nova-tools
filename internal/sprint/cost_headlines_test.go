package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
)

// The cost headline's priced denominator is independent of unpriced outcomes
// (docs/SPEC-SPRINT.md, cost headline coverage).
func TestAnUnpricedLandingCannotLowerPerLanded(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work), Now: t0}
	s.Work.SetRows([]string{"stream"})
	priced := &Card{ID: "priced", Row: "stream", Col: Landed, Fields: map[string]string{FieldCost: "5"}}
	book(priced, Consumer{Key: "priced#g1", Kind: "work", Usage: cardcost.ParseUsage("input=1 actual_usd=5 actual_by=harness")})
	s.Work.Put(priced)
	before := StreamTierCosts(s)["stream"].PerLanded
	unpriced := &Card{ID: "unpriced", Row: "stream", Col: Landed, Fields: map[string]string{}}
	book(unpriced, Consumer{Key: "unpriced#g1", Kind: "work", Usage: cardcost.ParseUsage("unpriced=no-tokens")})
	s.Work.Put(unpriced)
	assert.Equal(t, "$5.00", before)
	assert.Equal(t, before, StreamTierCosts(s)["stream"].PerLanded)
}

func TestCostHeadlinesKeepPriceSourcesAndDenominators(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work), Now: t0}
	s.Work.SetRows([]string{"stream"})
	c := &Card{ID: "card", Row: "stream", Col: Landed, Fields: map[string]string{FieldCost: "6"}}
	book(c,
		Consumer{Key: "actual", Kind: "work", Route: "route", Tier: "pro", Usage: cardcost.ParseUsage("input=1 actual_usd=4 predicted_usd=3")},
		Consumer{Key: "estimated", Kind: "read", Route: "route", Tier: "pro", Usage: cardcost.ParseUsage("input=1 predicted_usd=2")},
		Consumer{Key: "unknown", Kind: "read", Route: "route", Tier: "pro", Usage: cardcost.ParseUsage("unpriced=no-tokens")})
	s.Work.Put(c)
	got := StreamTierCosts(s)["stream"]
	assert.Equal(t, PriceCoverage{All: 3, Priced: 2, Actual: 1, Estimated: 1, Unpriced: 1}, got.Coverage)
	assert.Equal(t, got.Coverage, got.TierCoverage["pro"])
	assert.Equal(t, "$6.00", got.TotalCost)
	assert.Equal(t, "-", got.PerLanded, "a partial outcome is not a fully priced landing")
	assert.Equal(t, PriceCoverage{All: 1, Unpriced: 1}, got.LandedCoverage)
	assert.Equal(t, "$3.00", got.Routes["route"].PerPriced, "two priced runs, never three")
	assert.Equal(t, "completed-recorded-runs", got.Routes["route"].Stage)
	assert.Contains(t, got.AccountingScope, "excludes-provider-gap")
	assert.Equal(t, "unknown", got.PerVerifiedDev, "landed does not prove dev ancestry")
}

func TestCostHeadlinesRetainCoveragePastTheDetailBound(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work), Now: t0}
	s.Work.SetRows([]string{"stream"})
	c := &Card{ID: "card", Row: "stream", Col: Working, Fields: map[string]string{}}
	for n := 0; n < MaxCostRecords+1; n++ {
		book(c, Consumer{Key: itoa(n), Kind: "work", Route: "route", Tier: "pro", Usage: cardcost.ParseUsage("input=1 actual_usd=1")})
	}
	s.Work.Put(c)
	got := StreamTierCosts(s)["stream"]
	assert.Equal(t, MaxCostRecords+1, got.Coverage.Priced)
	assert.Equal(t, 1, got.UnattributedRuns)
	assert.Equal(t, PriceCoverage{All: 1, Priced: 1, Actual: 1}, got.TierCoverage["unattributed"])
	assert.Equal(t, "$1.00", got.CostByTier["unattributed"])
	assert.Equal(t, "$1.00", got.Routes["unattributed"].USD)
}

package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// repriceWorld is a sprint whose flash route was priced wrong for days (input $0.03/M and
// output $0.50/M against the list's $0.30 and $1.20, the deepseek-v4.1-flash rows of
// 2026-10-01) and is now right: a landed primary with two takes priced at the old sheet, a
// working primary with a take and a read at the old sheet, a run that reported no token
// and a take on the pro route, whose prices never changed. The stream's control card holds
// the landed card's cost.
func repriceWorld(t *testing.T) (*world, Route, Route) {
	w := newWorld(t)
	old := Route{Name: "flash-ds", Provider: "openrouter", Model: "deepseek/v4.1-flash", Enabled: true,
		Prices: cardcost.Prices{Input: "0.03", Output: "0.5", ReasoningAsOutput: true, AsOf: "2026-10-01"}}
	pro := Route{Name: "pro-a", Provider: "openrouter", Model: "deepseek/v4-pro", Enabled: true,
		Prices: cardcost.Prices{Input: "1", Output: "2", ReasoningAsOutput: true, AsOf: "2026-09-20"}}
	w.s.Routes = []Route{old, pro}
	w.s.Work.SetRows([]string{"s1"})
	w.s.Merge.SetRows([]string{"s1"})
	priced := func(r Route, usage string) cardcost.Usage {
		return cardcost.ParseUsage(usage).Priced(r.Name, r.Prices)
	}
	day := func(d int) string { return stamp(w.s.Now.Add(-time.Duration(d) * 24 * time.Hour)) }

	landed := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{}}
	book(landed,
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g0", Route: "flash-ds", Tier: "flash", End: "failed", At: day(4),
			Usage: priced(old, "input=1000000 output=1000000")}, // 0.03 + 0.5 = 0.53, at the list 1.5
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g1", Route: "flash-ds", Tier: "flash", End: "ok", At: day(3),
			Usage: priced(old, "input=2000000 output=0")}, // 0.06, at the list 0.6
	)
	landed.Fields[FieldCost] = cardcost.ParseTotal(landed.F(FieldCostTotal)).Charged
	w.s.Work.Put(landed)

	working := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(working,
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g0", Route: "flash-ds", Tier: "flash", End: "no result", At: day(2),
			Usage: priced(old, "input=0 output=2000000")}, // 1, at the list 2.4
		Consumer{Kind: "read", Card: "s1-2.r1", Key: "s1-2.r1#v", Model: "deepseek/v4.1-flash", Tier: "flash", End: "ok", At: day(2),
			Usage: priced(old, "input=1000000 output=0")}, // 0.03, at the list 0.3
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g1", Route: "flash-ds", Tier: "flash", End: "no result", At: day(1),
			Usage: cardcost.Usage{Tokens: cardcost.None(), Wait: cardcost.Unreported, Run: cardcost.Unreported, Unpriced: cardcost.WhyNoTokens}},
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g2", Route: "pro-a", Tier: "pro", End: "ok", At: day(1),
			Usage: priced(pro, "input=1000000 output=1000000")}, // 3, unchanged
	)
	w.s.Work.Put(working)
	w.s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Fields: map[string]string{FieldCost: landed.F(FieldCost)}})

	fixed := old
	fixed.Prices.Input, fixed.Prices.Output, fixed.Prices.AsOf = "0.3", "1.2", "2026-10-05"
	w.s.Routes = []Route{fixed, pro}
	return w, fixed, pro
}

// Every priced consumer record is computed again from its stored tokens at its route's
// current prices: the record's predicted cost and its copy of the sheet, each primary's
// total, a landed primary's cost and its stream's sum on the control card, and the where
// view's per-stream spend all move to the repriced figures; the report says, per route, the
// old sum, the new sum, the records and the price_as_of it used, one log line each; a run
// that reported no token is left as it is and counted; --route and --since narrow it.
func TestRepriceRecomputesEveryPricedRecordFromItsTokens(t *testing.T) {
	t.Parallel()
	w, _, _ := repriceWorld(t)
	before := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, "$4.62", before.TotalCost) // 0.53 + 0.06 + 1 + 0.03 + 3

	p, rep := RepriceOf(w.s, RepriceReq{})
	require.Empty(t, p.Refused)
	require.Len(t, rep.Routes, 2)
	flash, pro := rep.Routes[0], rep.Routes[1]
	assert.Equal(t, RepriceRoute{Route: "flash-ds", AsOf: "2026-10-05", Records: 4, Changed: 4,
		OldPredicted: "1.62", NewPredicted: "4.8", OldCharged: "1.62", NewCharged: "4.8"}, flash)
	assert.Equal(t, RepriceRoute{Route: "pro-a", AsOf: "2026-09-20", Records: 1, Changed: 0,
		OldPredicted: "3", NewPredicted: "3", OldCharged: "3", NewCharged: "3"}, pro)
	assert.Equal(t, 1, rep.NoTokens, "the run with no token is counted")
	assert.Equal(t, 2, rep.Cards)
	assert.Equal(t, 1, rep.Streams)

	var logged []string
	for _, u := range p.Units {
		if u.Key == RepriceKey {
			logged = append(logged, u.Moved)
		}
	}
	assert.Equal(t, []string{
		"reprice route=flash-ds price_as_of=2026-10-05 records=4 changed=4 old_usd=1.62 new_usd=4.8 old_predicted_usd=1.62 new_predicted_usd=4.8",
		"reprice route=pro-a price_as_of=2026-09-20 records=1 changed=0 old_usd=3 new_usd=3 old_predicted_usd=3 new_predicted_usd=3",
	}, logged)

	w.must(p)
	landed := CardCostOf(w.s.Work.Placed("s1-1"))
	assert.Equal(t, "2.1", landed.Total.Charged)
	assert.Equal(t, "2.1", landed.Total.Predicted)
	assert.Equal(t, 2, landed.Total.PredOf)
	assert.Equal(t, int64(3000000), landed.Total.Tokens.Input, "the tokens are as they were")
	assert.Equal(t, "2.1", w.s.Work.Placed("s1-1").F(FieldCost))
	assert.Equal(t, "2.1", w.s.StreamCtl("s1").F(FieldCost))
	for _, c := range landed.Consumers {
		assert.Contains(t, c.Usage.Prices, "in:0.3,out:1.2", c.Key)
		assert.Contains(t, c.Usage.Prices, "asof:2026-10-05", c.Key)
	}
	working := CardCostOf(w.s.Work.Placed("s1-2"))
	assert.Equal(t, "5.7", working.Total.Charged) // 2.4 + 0.3 + 3
	assert.Equal(t, 4, working.Total.Records)
	assert.Equal(t, 3, working.Total.ChargedOf)
	for _, c := range working.Consumers {
		if c.Key == "s1-2#g1" {
			assert.Equal(t, cardcost.WhyNoTokens, c.Usage.Unpriced, "left as it is")
			assert.Empty(t, c.Usage.Predicted)
		}
	}
	after := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, "$7.80", after.TotalCost)
	assert.Equal(t, "$4.80", after.CostByTier["flash"])
	assert.Equal(t, "$3.00", after.CostByTier["pro"])
	assert.Equal(t, 1, after.UnpricedRuns)

	again, rep2 := RepriceOf(w.s, RepriceReq{})
	assert.Equal(t, 0, rep2.Cards, "a second reprice at the same prices changes nothing")
	for _, u := range again.Units {
		assert.Empty(t, u.Changes, u.Key)
	}

	w2, _, _ := repriceWorld(t)
	_, only := RepriceOf(w2.s, RepriceReq{Routes: []string{"pro-a"}})
	require.Len(t, only.Routes, 1)
	assert.Equal(t, "pro-a", only.Routes[0].Route)
	_, since := RepriceOf(w2.s, RepriceReq{Since: stamp(w2.s.Now.Add(-50 * time.Hour))})
	require.Len(t, since.Routes, 2)
	assert.Equal(t, 2, since.Routes[0].Records, "the take and the read of two days ago, not the landed card's")
	assert.Equal(t, "1.03", since.Routes[0].OldCharged)
	assert.Equal(t, "2.7", since.Routes[0].NewCharged)
	bad, _ := RepriceOf(w2.s, RepriceReq{Since: "yesterday"})
	require.Len(t, bad.Refused, 1)
}

// A record the harness priced keeps the harness's figure as its charged cost: the reprice
// moves its prediction and counts it held, and a record whose route is no longer in the
// store is left and named.
func TestRepriceKeepsTheHarnessFigureAndNamesAGoneRoute(t *testing.T) {
	t.Parallel()
	w, fixed, _ := repriceWorld(t)
	pr := w.s.Work.Placed("s1-2")
	book(pr,
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g3", Route: "flash-ds", Tier: "flash", End: "ok", At: stamp(w.s.Now),
			Usage: cardcost.ParseUsage("input=1000000 output=0 actual_usd=0.25 actual_by=harness").Priced("flash-ds", cardcost.Prices{Input: "0.03"})},
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g4", Route: "gone", Tier: "flash", End: "ok", At: stamp(w.s.Now),
			Usage: cardcost.ParseUsage("input=1000000").Priced("gone", cardcost.Prices{Input: "0.03"})},
	)
	_ = fixed
	p, rep := RepriceOf(w.s, RepriceReq{})
	require.Empty(t, p.Refused)
	assert.Equal(t, 1, rep.Routes[0].HeldByActual)
	assert.Equal(t, map[string]int{"gone": 1}, rep.RouteGone)
	w.must(p)
	tot := CardCostOf(w.s.Work.Placed("s1-2")).Total
	assert.Equal(t, "5.98", tot.Charged, "2.4 + 0.3 + 3 + the harness's 0.25 + the gone route's 0.03")
}

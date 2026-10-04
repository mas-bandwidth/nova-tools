package sprint

import (
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The owner, 2026-10-04 4:22 PM: "Fix it so it stops happening". Every writer of a cost
// record (addConsumer) writes it with one of the four tiers and keeps the primary's tier
// totals (FieldCostTier) adding up to its charged figure; a record with no tier is refused.

// tierUsage is a usage line a member or reader reports, with a harness cost.
const tierUsage = "input=100 output=10 model=opencode/qwen3.8-flash actual_usd=0.25 actual_by=harness"

// requireTiered fails unless every record on the primary has a tier and its tier totals
// add up to its charged figure exactly.
func requireTiered(t *testing.T, pr *Card, writer string) {
	t.Helper()
	v := CardCostOf(pr)
	require.NotEmpty(t, v.Consumers, "%s wrote no record", writer)
	for _, con := range v.Consumers {
		assert.True(t, IsTier(con.Tier), "%s wrote %s with tier %q", writer, con.Key, con.Tier)
	}
	stored, ok := storedTierTotals(pr)
	assert.True(t, ok, "%s: the tier totals %v do not add up to charged %s", writer, stored, v.Total.Charged)
}

func TestAddConsumerRefusesARecordWithoutATier(t *testing.T) {
	t.Parallel()
	pr := &Card{ID: "s1-1", Fields: map[string]string{}}
	set := map[string]string{}
	for _, tier := range []string{"", "untiered", "-"} {
		assert.False(t, addConsumer(&Snapshot{}, pr, set, Consumer{Kind: "work", Key: "k", Tier: tier, Usage: cardcost.ParseUsage(tierUsage)}), "tier %q", tier)
		assert.Empty(t, set, "a refused record writes nothing")
	}
	require.True(t, addConsumer(&Snapshot{}, pr, set, Consumer{Kind: "work", Key: "k", Tier: "flash", Usage: cardcost.ParseUsage(tierUsage)}))
	assert.Equal(t, "0.25", set[FieldCostTier+"flash"])
}

// A primary holding records from before the tier totals has them seeded at its next
// record, so its totals cover every record it holds.
func TestAddConsumerSeedsTheTotalsOfAnOlderCard(t *testing.T) {
	t.Parallel()
	pr := &Card{ID: "s1-1", Fields: map[string]string{"brief": "RESULT: s1-1 tier: pro"}}
	old := Consumer{Kind: "work", Key: "old", Route: "pro-x", Usage: cardcost.ParseUsage("input=1 actual_usd=1 actual_by=harness")}
	pr.Fields[FieldCostRecord+"old"] = old.line()
	pr.Fields[FieldCostTotal] = cardcost.NoTotal().Add(old.Usage).String()
	set := map[string]string{}
	require.True(t, addConsumer(&Snapshot{}, pr, set, Consumer{Kind: "read", Key: "new", Tier: "flash", Usage: cardcost.ParseUsage(tierUsage)}))
	_, whole := storedTierTotals(withFields(pr, set, nil))
	assert.True(t, whole, "the totals cover the older record too (costs retier writes its tier word)")
	assert.Equal(t, "1", set[FieldCostTier+"pro"], "the older record by its route name")
	assert.Equal(t, "0.25", set[FieldCostTier+"flash"])
}

// Every writer: a finish ok, a failed finish, a take the provider failed, a launch
// refused at staging, a read's verdict and a read handed back.
func TestEveryCostWriterWritesATier(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 4}}))
	work := func(id string) *Card { return w.s.Fleet.Card(w.s.Work.Card(id).F("work")) }
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		c := work(id)
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
	finish := func(id string, failed bool, report, writer string) {
		c := work(id)
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: failed, Report: report, Usage: tierUsage}))
		requireTiered(t, w.s.Work.Card(id), writer)
	}
	finish("s1-1", false, "", "finish ok")
	finish("s1-2", true, "tests red", "a failed finish")
	finish("s1-3", true, cardhdr.EndProvider+": 529", "a take the provider failed")
	finish("s1-4", true, cardhdr.EndStaging+": no disk", "a launch refused at staging")
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.GreaterOrEqual(t, len(reads), 2)
	w.must(Read(w.s, ReadReq{As: reads[0].F("reader"), Return: true, Reason: "no verdict", Usage: tierUsage, Sel: Sel{IDs: []string{reads[0].ID}}}))
	requireTiered(t, w.s.Work.Card("s1-1"), "a read handed back")
	w.must(Read(w.s, ReadReq{As: reads[1].F("reader"), Verdict: "ok", Usage: tierUsage, Sel: Sel{IDs: []string{reads[1].ID}}}))
	requireTiered(t, w.s.Work.Card("s1-1"), "a read's verdict")
}

// The read-time rule is a guard: a landed card whose tier totals add up to its cost is
// split by them and the guard stays quiet; one without them is split by the rule and the
// guard counts it, which where raises as an alarm.
func TestTheReadTimeRuleIsAGuardThatCounts(t *testing.T) {
	t.Parallel()
	w := NewTable(Work)
	w.SetRows([]string{"s1"})
	w.Put(&Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief": "RESULT: s1-1 tier: pro", FieldCost: "3", FieldCostTotal: "records=2 charged_usd=3 charged_of=2",
		FieldCostTier + "flash": "1", FieldCostTier + "pro": "2",
	}})
	got := StreamTierCosts(&Snapshot{Work: w}, TierRules{})["s1"]
	assert.Equal(t, map[string]string{"flash": "$1.00", "pro": "$2.00"}, got.CostByTier)
	assert.Empty(t, got.Guard, "the stored totals: no guard")
	w.Put(&Card{ID: "s1-2", Row: "s1", Col: Landed, Fields: map[string]string{"brief": "RESULT: s1-2 tier: pro", FieldCost: "0.5"}})
	got = StreamTierCosts(&Snapshot{Work: w}, TierRules{})["s1"]
	assert.Equal(t, map[string]string{"flash": "$1.00", "pro": "$2.50"}, got.CostByTier)
	assert.Equal(t, "$0.50", got.Guard)
	assert.Equal(t, 1, got.GuardCards)
}

// A landing whose card holds a charged cost and no tier totals raises "a landed cost has
// no tier", naming the card; a card with its totals raises none.
func TestALandingWithoutTierTotalsRaisesTheAlarm(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for _, id := range []string{"s1-1", "s1-2"} {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Usage: tierUsage}))
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for _, id := range []string{"s1-1", "s1-2"} {
		for _, rc := range readsAt(w.s, w.s.Work.Card(id), 1) {
			w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for k := range w.s.Work.Card("s1-2").Fields { // s1-2 as a card written before the totals
		if strings.HasPrefix(k, FieldCostTier) {
			delete(w.s.Work.Card("s1-2").Fields, k)
		}
	}
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2}))
	notes := w.notesOf(NCostNoTier)
	require.Len(t, notes, 1)
	assert.Equal(t, []string{"s1-2"}, notes[0].Primaries)
}

// SplitCents never loses a cent of an exact sum: the parts add up to the sum rounded up.
func TestSplitCentsKeepsEveryCent(t *testing.T) {
	t.Parallel()
	by := map[string]*big.Rat{"flash": big.NewRat(1, 3), "pro": big.NewRat(1, 3), "heavy": big.NewRat(1, 3), "frontier": big.NewRat(1, 1000)}
	sum := new(big.Rat)
	for _, v := range SplitCents(by) {
		r, _ := new(big.Rat).SetString(v[1:])
		sum.Add(sum, r)
	}
	assert.Equal(t, cardcost.Cents(sumOf(by)), cardcost.Cents(sum))
}

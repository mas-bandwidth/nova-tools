package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// Cost headlines carry their completeness (docs/SPEC-SPRINT.md section 1, cost visibility;
// the 2026-10-06 nova-sprint review, item 6). An unpriced landing is not in the per-landed
// ratio, and a card taken off the table keeps its spend in the stream.

// landedWith is a landed primary of stream s1 carrying the usages as its takes, its cost
// field the charged figure as landing writes it.
func landedWith(id string, usages ...string) *Card {
	c := &Card{ID: id, Row: "s1", Col: Landed, Fields: map[string]string{}}
	for i, u := range usages {
		book(c, Consumer{Kind: "work", Card: id, Key: id + "#g" + itoa(i+1), End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage(u)})
	}
	if ch := CardCostOf(c).Total.Charged; ch != "" {
		c.Fields[FieldCost] = ch
	}
	return c
}

func costWorld(cards ...*Card) *Snapshot {
	s := &Snapshot{Now: t0, Fleet: NewTable(Fleet), Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1"})
	for _, c := range cards {
		s.Work.Put(c)
	}
	return s
}

func TestAnUnpricedLandingCannotLowerPerLanded(t *testing.T) {
	t.Parallel()
	priced := landedWith("s1-1", "input=1 actual_usd=4 actual_by=harness")
	before := StreamTierCosts(costWorld(priced))["s1"]
	assert.Equal(t, "$4.00", before.PerLanded)

	// a landing with no cost at all, and one priced in part: neither may make the stream read cheaper
	unpriced := landedWith("s1-2", "unpriced=no-tokens")
	partial := landedWith("s1-3", "input=1 actual_usd=0.10 actual_by=harness", "unpriced=no-tokens")
	after := StreamTierCosts(costWorld(priced, unpriced, partial))["s1"]
	assert.Equal(t, "$4.00", after.PerLanded, "the per-landed figure is over the cards priced whole, never lowered by an unpriced one")
}

// Every headline carries its denominators and coverage, and the figure an unpriced record
// would make smaller reads unknown: the per-landed cost when no landed card is priced whole,
// the spend per landed card while any record is unpriced.
func TestCostHeadlinesCarryTheirDenominatorsAndCoverage(t *testing.T) {
	t.Parallel()
	priced := landedWith("s1-1", "input=1 actual_usd=4 actual_by=harness", "input=10 predicted_usd=1")
	unpriced := landedWith("s1-2", "unpriced=no-tokens")
	working := &Card{ID: "s1-3", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(working, Consumer{Kind: "work", Card: "s1-3", Key: "s1-3#g1", End: "no result", At: stamp(t0), Usage: cardcost.ParseUsage("input=10 predicted_usd=2")})
	tc := StreamTierCosts(costWorld(priced, unpriced, working))["s1"]
	assert.Equal(t, 2, tc.Landed)
	assert.Equal(t, 1, tc.LandedPriced)
	assert.Equal(t, "$5.00", tc.PerLanded)
	assert.Equal(t, Coverage{Records: 3, Actual: 1, Estimated: 1, Unpriced: 1}, tc.LandedCoverage)
	assert.Equal(t, Coverage{Records: 4, Actual: 1, Estimated: 2, Unpriced: 1}, tc.Coverage)
	assert.Equal(t, 1, tc.UnpricedRuns)
	assert.Equal(t, "$7.00", tc.TotalCost, "the priced spend: a floor while a record is unpriced")
	assert.Equal(t, CostUnknown, tc.SpendPerLanded, "unknown spend is shown as unknown")
	assert.Equal(t, "1 actual · 2 estimated · 0 tokens · 1 unpriced of 4 records", tc.Coverage.Text())

	// nothing priced whole: unknown, never a number
	tc = StreamTierCosts(costWorld(unpriced))["s1"]
	assert.Equal(t, CostUnknown, tc.PerLanded)
	assert.Equal(t, CostUnknown, tc.SpendPerLanded)
	// every record priced: the spend per landed card is every record's over the landed cards
	tc = StreamTierCosts(costWorld(priced, working))["s1"]
	assert.Equal(t, "$5.00", tc.PerLanded, "the landed card's own records")
	assert.Equal(t, "$7.00", tc.SpendPerLanded, "every record of the stream over its one landed card")
	// nothing landed
	tc = StreamTierCosts(costWorld(working))["s1"]
	assert.Equal(t, "-", tc.PerLanded)
	assert.Equal(t, "-", tc.SpendPerLanded)
}

// Dropping or re-cutting a card keeps its spend in the stream's lineage (DroppedSpendFields
// on the control card, as the drop writes it): the stream's spend per landed card cannot
// improve by taking a costly card off the table.
func TestADroppedCardsSpendStaysInTheStream(t *testing.T) {
	t.Parallel()
	landed := landedWith("s1-1", "input=1 actual_usd=1 actual_by=harness")
	costly := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(costly,
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g1", End: "failed", At: stamp(t0), Usage: cardcost.ParseUsage("input=1 actual_usd=6 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g2", End: "failed", At: stamp(t0), Usage: cardcost.ParseUsage("input=1 predicted_usd=2")},
	)
	s := costWorld(landed, costly)
	s.Merge = NewTable(Merge)
	s.Merge.SetRows([]string{"s1"})
	ctl := &Card{ID: CtlID("s1"), Row: "s1", Col: "control", Fields: map[string]string{}}
	s.Merge.Put(ctl)
	before := StreamTierCosts(s)["s1"]
	assert.Equal(t, "$9.00", before.SpendPerLanded)

	// the drop: the card leaves the table, its spend lands on the control card
	set := DroppedSpendFields(ctl, []*Card{costly, {ID: "s1-9", Fields: map[string]string{}}})
	assert.Equal(t, map[string]string{FieldDroppedCards: "1", FieldDroppedCost: "8", FieldDroppedCover: "records=2 actual=1 estimated=1 tokens=0 unpriced=0"}, set,
		"a card with no record adds nothing")
	for k, v := range set {
		ctl.Fields[k] = v
	}
	s.Work.Drop("s1-2")
	after := StreamTierCosts(s)["s1"]
	assert.Equal(t, before.SpendPerLanded, after.SpendPerLanded, "dropping the costly card does not make the stream read cheaper")
	assert.Equal(t, before.TotalCost, after.TotalCost)
	assert.Equal(t, before.Coverage, after.Coverage)
	assert.Equal(t, DroppedSpend{Cards: 1, Cost: "8", Coverage: Coverage{Records: 2, Actual: 1, Estimated: 1}}, after.Dropped)
	assert.Equal(t, "$1.00", after.PerLanded, "the landed card's own cost, in its own scope")

	// a second drop adds to the first, exactly
	unpriced := &Card{ID: "s1-3", Fields: map[string]string{}}
	book(unpriced, Consumer{Kind: "work", Card: "s1-3", Key: "s1-3#g1", End: "no result", At: stamp(t0), Usage: cardcost.ParseUsage("unpriced=no-tokens")})
	for k, v := range DroppedSpendFields(ctl, []*Card{unpriced}) {
		ctl.Fields[k] = v
	}
	again := StreamTierCosts(s)["s1"]
	assert.Equal(t, CostUnknown, again.SpendPerLanded, "a dropped card's unpriced run is unknown spend still")
	assert.Equal(t, 1, again.UnpricedRuns)
	assert.Equal(t, 2, again.Dropped.Cards)
	assert.Equal(t, "$9.00", again.TotalCost)
}

// The sprint's headlines are every stream's counted as one: the same stage and scope as a
// stream's, its denominators exact.
func TestTheSprintsHeadlinesCountEveryStreamAsOne(t *testing.T) {
	t.Parallel()
	a := landedWith("s1-1", "input=1 actual_usd=1 actual_by=harness")
	b := landedWith("s2-1", "input=1 actual_usd=2 actual_by=harness")
	b.Row = "s2"
	s := costWorld(a, b)
	s.Work.SetRows([]string{"s1", "s2"})
	tc := SprintTierCosts(s)
	assert.Equal(t, 2, tc.Landed)
	assert.Equal(t, 2, tc.LandedPriced)
	assert.Equal(t, "$1.50", tc.PerLanded)
	assert.Equal(t, "$1.50", tc.SpendPerLanded)
	assert.Equal(t, "$3.00", tc.TotalCost)
}

// A route's or a tier's dollars say how many of its runs no dollar was charged for.
func TestUnpricedRunsAreCountedByTierAndRoute(t *testing.T) {
	t.Parallel()
	c := &Card{ID: "s1-1", Row: "s1", Col: Review, Fields: map[string]string{}}
	day := stamp(t0)
	book(c,
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g1", Tier: "pro", End: "ok", At: day, Usage: cardcost.ParseUsage("input=1 actual_usd=1 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g2", Tier: "pro", End: "no result", At: day, Usage: cardcost.ParseUsage("unpriced=no-tokens")},
		Consumer{Kind: "read", Card: "s1-1.r1", Key: "s1-1.r1#v", Tier: "pro", End: "ok", At: day, Usage: cardcost.ParseUsage("input=100 price_route=pro-a predicted_usd=1")},
		Consumer{Kind: "read", Card: "s1-1.r2", Key: "s1-1.r2#v", Tier: "pro", End: "ok", At: day, Usage: cardcost.ParseUsage("input=100 price_route=pro-a unpriced=no-price:input")},
	)
	tc := StreamTierCosts(costWorld(c))["s1"]
	assert.Equal(t, map[string]string{"pro": "$2.00"}, tc.CostByTier)
	assert.Equal(t, map[string]int{"pro": 2}, tc.UnpricedByTier, "the take with no usage and the read its route could not price")
	assert.Equal(t, "reads today: pro-a $1.00 2 reads (1 unpriced) 200 tokens", ReadSpendLine(map[string]TierCosts{"s1": tc}))
}

// A card the snapshot still holds after its place is cleared keeps its spend in the stream,
// attributed by the stream field. Once the control card records that spend, the card is not
// counted again.
func TestACardKeptOffTheTableStaysInTheStream(t *testing.T) {
	t.Parallel()
	landed := landedWith("s1-1", "input=1 actual_usd=1 actual_by=harness")
	costly := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{"stream": "s1"}}
	book(costly, Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g1", End: "failed", At: stamp(t0), Usage: cardcost.ParseUsage("input=1 actual_usd=6 actual_by=harness")})
	empty := &Card{ID: "s1-0", Fields: map[string]string{"stream": "s1"}}
	s := costWorld(landed, costly, empty)
	before := StreamTierCosts(s)["s1"]

	s.Work.Drop(costly.ID)
	costly.Row, costly.Col = "", ""
	s.Work.Put(costly)
	after := StreamTierCosts(s)["s1"]
	assert.Equal(t, before.TotalCost, after.TotalCost, "taking the card off the table does not drop its spend")
	assert.Equal(t, before.SpendPerLanded, after.SpendPerLanded)
	assert.Equal(t, before.Coverage, after.Coverage)
	assert.Equal(t, before.PerLanded, after.PerLanded)
	assert.Equal(t, DroppedSpend{Cards: 1, Cost: "6", Coverage: Coverage{Records: 1, Actual: 1}}, after.Dropped, "a card with no record adds nothing")

	s.Merge = NewTable(Merge)
	s.Merge.SetRows([]string{"s1"})
	ctl := &Card{ID: CtlID("s1"), Row: "s1", Col: "control", Fields: map[string]string{}}
	s.Merge.Put(ctl)
	for k, v := range DroppedSpendFields(ctl, []*Card{costly}) {
		ctl.Fields[k] = v
	}
	again := StreamTierCosts(s)["s1"]
	assert.Equal(t, before.TotalCost, again.TotalCost, "the control card and the card still held are one spend, not two")
	assert.Equal(t, 1, again.Dropped.Cards)
}

// Drop writes the leaving cards' spend onto the stream control card, so the stream's total
// keeps it once after the card is off the table.
func TestDropKeepsSpendOnTheControlCard(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	c := w.s.Work.Card("s1-1")
	book(c, Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g1", End: "failed", At: stamp(w.s.Now), Usage: cardcost.ParseUsage("input=1 actual_usd=6 actual_by=harness")})
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "the spend stays"}))
	ctl := w.s.StreamCtl("s1")
	assert.Equal(t, "1", ctl.F(FieldDroppedCards))
	assert.Equal(t, "6", ctl.F(FieldDroppedCost))
	assert.Contains(t, ctl.F(FieldDroppedCover), "records=1")
	tc := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, "$6.00", tc.TotalCost, "the control card holds the spend once")
	assert.Equal(t, 1, tc.Dropped.Cards)
	assert.Equal(t, map[string]string{"lineage": "$6.00"}, tc.CostByTier)
}

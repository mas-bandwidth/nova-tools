package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// landedWith is a landed primary of stream s1 with the usage records given, its cost the
// total's charged figure as the landing writes it (steps_merge.go).
func landedWith(id string, usages ...string) *Card {
	c := &Card{ID: id, Row: "s1", Col: Landed, Fields: map[string]string{}}
	for i, u := range usages {
		book(c, Consumer{Kind: "work", Card: id, Key: id + "#g" + itoa(i+1), End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage(u)})
	}
	if v := CardCostOf(c).Total.Charged; v != "" {
		c.Fields[FieldCost] = v
	}
	return c
}

func costSnapshot(cards ...*Card) *Snapshot {
	s := &Snapshot{Now: t0, Fleet: NewTable(Fleet), Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1"})
	for _, c := range cards {
		s.Work.Put(c)
	}
	return s
}

// A cost headline carries its denominators and its coverage, and a landed card with any
// unpriced record is in neither the sum nor the count of the per-landed figure: an unpriced
// completion cannot make the stream look cheaper (docs/SPEC-SPRINT.md section 1, cost
// visibility). The live stream that showed per_landed $1.20 beside 19 unpriced runs divided
// the priced landed cost by every landed card.
func TestAnUnpricedLandingCannotLowerPerLanded(t *testing.T) {
	t.Parallel()
	priced := landedWith("s1-1", "input=1 actual_usd=3 actual_by=harness", "input=1 predicted_usd=1")
	tc := StreamTierCosts(costSnapshot(priced))["s1"]
	require.Equal(t, "$4.00", tc.PerLanded, "one landed card priced whole")
	require.Equal(t, "$4.00", tc.SpendPerLanded)
	for _, c := range []struct {
		name  string
		more  *Card
		cover Coverage
	}{
		{"a landing with no cost", landedWith("s1-2", "unpriced=no-tokens"),
			Coverage{Records: 3, Actual: 1, Estimated: 1, Unpriced: 1}},
		{"a landing priced in part", landedWith("s1-2", "input=1 predicted_usd=0.10", "unpriced=no-tokens"),
			Coverage{Records: 4, Actual: 1, Estimated: 2, Unpriced: 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tc := StreamTierCosts(costSnapshot(priced, c.more))["s1"]
			assert.Equal(t, "$4.00", tc.PerLanded, "the unpriced landing is in neither the sum nor the count")
			assert.Equal(t, 2, tc.Landed, "every landed card is the denominator's all")
			assert.Equal(t, 1, tc.LandedPriced, "the landed cards priced whole")
			assert.Equal(t, c.cover, tc.LandedCoverage)
			assert.Equal(t, c.cover, tc.Coverage, "every record of the stream: here, the landed ones")
			assert.Equal(t, 1, tc.UnpricedRuns)
			assert.Equal(t, CostUnknown, tc.SpendPerLanded, "an unpriced run is unknown spend, never free")
			assert.Equal(t, SpendPerLandedScope, tc.SpendScope)
		})
	}

	t.Run("nothing landed is priced whole", func(t *testing.T) {
		t.Parallel()
		tc := StreamTierCosts(costSnapshot(landedWith("s1-2", "unpriced=no-tokens")))["s1"]
		assert.Equal(t, CostUnknown, tc.PerLanded)
		assert.Equal(t, 1, tc.Landed)
		assert.Zero(t, tc.LandedPriced)
	})
	t.Run("nothing landed", func(t *testing.T) {
		t.Parallel()
		tc := StreamTierCosts(costSnapshot())["s1"]
		assert.Equal(t, "-", tc.PerLanded)
		assert.Equal(t, "-", tc.SpendPerLanded)
	})
}

// The spend per landed card counts every recorded take and read of the stream's cards in
// any column over its landed cards, so the spend on cards still open or failed is never
// left out of what a landing cost; a subscription read is tokens, priced, never unknown.
func TestSpendPerLandedCountsEveryRecordOfTheStream(t *testing.T) {
	t.Parallel()
	landed := landedWith("s1-1", "input=1 actual_usd=3 actual_by=harness")
	sub := Consumer{Kind: "read", Card: "s1-1.r1", Key: "s1-1.r1#v", End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage("input=5 unpriced=" + WhySubscription)}
	book(landed, sub)
	open := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(open, Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g1", End: "failed", At: stamp(t0), Usage: cardcost.ParseUsage("input=1 actual_usd=5 actual_by=harness")})
	tc := StreamTierCosts(costSnapshot(landed, open))["s1"]
	assert.Equal(t, "$3.00", tc.PerLanded, "the landed card's own records")
	assert.Equal(t, "$8.00", tc.SpendPerLanded, "every record of the stream over the one landed card")
	assert.Equal(t, Coverage{Records: 3, Actual: 2, Tokens: 1}, tc.Coverage)
	assert.Equal(t, "2 actual · 0 estimated · 1 tokens · 0 unpriced of 3 records", tc.Coverage.Text())
}

package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// Reads are priced like work (the owner, 2026-10-05: "do we have the cost for readers
// properly calculated yet in nova sprint?"; 3,939 read records then, none priced): a
// routed read's verdict carries its usage or is refused naming the remedy, a read with
// usage is priced from its read card's route row as a take is from its work card's, a
// subscription reader's read keeps its tokens with no dollar figure, and the card's
// complete cost sums its work and its reads.

// pricedRoute is the one pro route every take and read of the world runs on: $1 a
// million input tokens, $10 a million output tokens.
var pricedRoute = Route{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "openrouter", Model: "vendor/m", Enabled: true,
	Prices: cardcost.Prices{Input: "1", Output: "10", ReasoningAsOutput: true}}

// routedReads is s1-1 finished with a priced take, in review, asked of two readers on
// the route; it returns the two read cards.
func routedReads(t *testing.T) (w *world, reads []*Card) {
	t.Helper()
	w = setup(t, 1)
	w.s.Routes = []Route{pricedRoute}
	readersReadEveryTier(w) // a fleet row reads flash unless it says more (fleetReadsFlashOnly)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
	require.Equal(t, "pro-a", c.F(FieldRoute), "the take is dealt on the route")
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	// the take: 1,000,000 in and 100,000 out at the route's prices is $1 + $1 = $2
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Report: "r", Usage: "input=1000000 output=100000"}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}})) // a card's reads are asked together: both in one ask
	reads = readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2)
	for _, rc := range reads {
		require.Equal(t, "pro-a", rc.F(FieldRoute), "the read is asked on the route")
	}
	return w, reads
}

func TestAReadWithoutUsageIsRefusedAndAPricedReadSumsIntoTheCard(t *testing.T) {
	t.Parallel()
	w, reads := routedReads(t)
	a, b := reads[0], reads[1]

	// a routed read's verdict with no --usage at all is refused naming the remedy, and
	// changes nothing (a usage with no token keeps its verdict:
	// TestAFleetReadWithNoTokensKeepsItsVerdict)
	for _, usage := range []string{"", "  "} {
		p := Read(w.s, ReadReq{As: a.Row, Verdict: "ok", Usage: usage, Sel: Sel{IDs: []string{a.ID}}})
		requireNothingPlanned(t, p, "a read on route pro-a is priced as work is")
		assert.Contains(t, p.Refused[0].Why, "--usage '<the harness's own token report", "the refusal names the remedy")
		assert.Contains(t, p.Refused[0].Why, "read --as "+a.Row+" --ok "+a.ID)
		assert.Contains(t, p.Refused[0].Why, UsageSubscription, "and the subscription reader's word")
	}
	p := Read(w.s, ReadReq{As: a.Row, Verdict: "broken", Finding: "internal/x.go:3 is wrong: change it", Sel: Sel{IDs: []string{a.ID}}})
	requireNothingPlanned(t, p, "a read on route pro-a is priced as work is")

	// a return with no usage is still taken: a read that never ran has no tokens
	// (the member's staging and launch refusals); begin needs none
	w.must(Read(w.s, ReadReq{As: a.Row, Begin: true, Sel: Sel{IDs: []string{a.ID}}}))

	// with usage the read is priced from the read card's route row, as a take is from its
	// work card's: 500,000 in and 50,000 out is $0.5 + $0.5 = $1, whatever model the
	// harness says it ran
	w.must(Read(w.s, ReadReq{As: a.Row, Verdict: "ok", Usage: "input=500000 output=50000 model=opencode/other", Sel: Sel{IDs: []string{a.ID}}}))
	got := cardcost.ParseUsage(w.s.Readers.Card(a.ID).F(FieldUsage))
	assert.Equal(t, "pro-a", got.Route)
	assert.Equal(t, "1", got.Predicted)
	assert.Equal(t, cardcost.CostPredicted, got.Present())

	// a subscription reader's read keeps its tokens and no dollar figure, even the
	// notional one its harness printed
	w.must(Read(w.s, ReadReq{As: b.Row, Verdict: "ok", Usage: "input=200000 output=1000 actual_usd=0.9 actual_by=harness model=anthropic/claude " + UsageSubscription,
		Sel: Sel{IDs: []string{b.ID}}}))
	sub := cardcost.ParseUsage(w.s.Readers.Card(b.ID).F(FieldUsage))
	assert.Equal(t, WhySubscription, sub.Unpriced)
	assert.Empty(t, sub.Predicted)
	assert.Empty(t, sub.Actual)
	assert.Equal(t, int64(200000), sub.Tokens.Input)

	// the card's complete cost: the take and the priced read, each a record with its
	// model, route, tokens and cost; the subscription read is a record of tokens
	v := CardCostOf(w.s.Work.Card("s1-1"))
	require.Len(t, v.Consumers, 3)
	assert.Equal(t, 3, v.Total.Records)
	assert.Equal(t, "3", v.Total.Charged, "$2 of work and $1 of reads")
	assert.Equal(t, int64(1000000+500000+200000), v.Total.Tokens.Input)
	lines := v.CostLines()
	var readLines []string
	for _, l := range lines {
		if strings.HasPrefix(l, "COST kind=read") {
			readLines = append(readLines, l)
		}
	}
	require.Len(t, readLines, 2)
	priced, tokens := readLines[0], readLines[1]
	if strings.Contains(priced, "cost=tokens") {
		priced, tokens = tokens, priced
	}
	assert.Contains(t, priced, "route=pro-a model=opencode/other", "the model the harness ran, as a take's record keeps it")
	assert.Contains(t, priced, "input=500000 cache_read=- cache_write=- output=50000")
	assert.Contains(t, priced, "predicted_usd=1 ")
	assert.Contains(t, priced, "cost=predicted")
	assert.Contains(t, tokens, "input=200000")
	assert.Contains(t, tokens, "predicted_usd=- actual_usd=-")
	assert.Contains(t, tokens, "cost=tokens")

	// the where view's split: work and reads as their own numbers, the subscription
	// read's tokens, no run unpriced, and the day's read spend per route on one line
	tc := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, "$3.00", tc.TotalCost)
	assert.Equal(t, "$2.00", tc.WorkCost)
	assert.Equal(t, "$1.00", tc.ReadCost)
	assert.Equal(t, int64(201000), tc.ReadTokens)
	assert.Equal(t, 0, tc.UnpricedRuns)
	assert.Equal(t, 0, tc.ReadsNoTokens)
	assert.Equal(t, "reads today: pro-a $1.00 1 reads 550000 tokens · subscription tokens 1 reads 201000 tokens",
		ReadSpendLine(map[string]TierCosts{"s1": tc}))
}

// A read with no route (a store with no routes: the reader runs its own model) is taken
// with or without usage, as before.
func TestAnUnroutedReadIsTakenWithoutUsage(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	rc := readsAt(w.s, w.s.Work.Card("s1-1"), 1)[0]
	require.Empty(t, rc.F(FieldRoute))
	w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	assert.Equal(t, "", ReadUsageMissing(rc, "", "ok"))
}

// A fleet read whose harness reported no tokens (its member passes --usage with no token
// count: the harness printed none and its receipt held none) keeps its verdict, permissive
// in what we read: the verdict is recorded, its record says unpriced=no-tokens, and the
// stream counts it (ReadsNoTokens, reads_no_tokens in the where record), so the gap is
// seen and never silently lost; the day's read line is spend, and holds none of them.
func TestAFleetReadWithNoTokensKeepsItsVerdict(t *testing.T) {
	t.Parallel()
	w, reads := routedReads(t)
	a, b := reads[0], reads[1]
	w.must(Read(w.s, ReadReq{As: a.Row, Verdict: "ok", Usage: "wall=3s budget=unmetered usage_source=none", Sel: Sel{IDs: []string{a.ID}}}))
	w.must(Read(w.s, ReadReq{As: b.Row, Verdict: "broken", Finding: "internal/x.go:3 is wrong: change it", Usage: "usage_source=none", Sel: Sel{IDs: []string{b.ID}}}))
	ra, rb := w.s.Readers.Card(a.ID), w.s.Readers.Card(b.ID)
	assert.Equal(t, "ok", ra.F("verdict"), "the verdict is kept")
	assert.Equal(t, "broken", rb.F("verdict"), "the verdict is kept")
	for _, rc := range []*Card{ra, rb} {
		u := cardcost.ParseUsage(rc.F(FieldUsage))
		assert.Equal(t, cardcost.WhyNoTokens, u.Unpriced, rc.F(FieldUsage))
		assert.Equal(t, cardcost.CostNone, u.Present())
	}
	tc := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, 2, tc.ReadsNoTokens, "each tokenless read is counted")
	assert.Equal(t, 2, tc.UnpricedRuns)
	assert.Equal(t, "$2.00", tc.WorkCost)
	assert.Empty(t, tc.ReadCost)
	assert.Empty(t, ReadSpendLine(map[string]TierCosts{"s1": tc}), "no read spent anything today")
}

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// Reads are priced like work (internal/sprint/cost.go, readCostRecord and
// ReadUsageMissing) on the mem twin: a routed read's verdict with no --usage is refused
// and changes nothing, one with tokens is priced from its read card's route row and sums
// into the card's complete cost, and one whose harness reported no token keeps its
// verdict, recorded unpriced=no-tokens and counted.
func TestOnTheTwinARoutedReadIsPricedAndATokenlessOneKeepsItsVerdict(t *testing.T) {
	t.Parallel()
	pro := route("pro-a", "pro")
	pro.Prices = cardcost.Prices{Input: "1", Output: "10", ReasoningAsOutput: true}
	h := routeHarness(t, pro)
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "tester"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine() // asks both reads together
	reads := readsAt(h.snap(), h.snap().Work.Card("s1-1"))
	require.Len(t, reads, 2)
	a, b := reads[0], reads[1]
	require.Equal(t, "pro-a", a.F(sprint.FieldRoute))
	before := sprint.CardCostOf(h.snap().Work.Card("s1-1")).Total.Records

	// no --usage: refused, the remedy named, the store unchanged
	res := h.run(ReadStep(sprint.ReadReq{As: a.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{a.ID}}}))
	require.Len(t, res.Refused, 1)
	assert.Contains(t, res.Refused[0].Why, "has no --usage")
	assert.Contains(t, res.Refused[0].Why, "--usage '<the harness's own token report")
	assert.Equal(t, sprint.Asked, h.snap().Readers.Card(a.ID).Col)

	// tokens: priced from the route row, $0.5 + $0.5
	h.must(ReadStep(sprint.ReadReq{As: a.Row, Verdict: "ok", Usage: "input=500000 output=50000 model=other/m", Sel: sprint.Sel{IDs: []string{a.ID}}}))
	ua := cardcost.ParseUsage(h.snap().Readers.Card(a.ID).F(sprint.FieldUsage))
	assert.Equal(t, "pro-a", ua.Route)
	assert.Equal(t, "1", ua.Predicted)

	// no token reported: the verdict is kept and recorded unpriced=no-tokens
	h.must(ReadStep(sprint.ReadReq{As: b.Row, Verdict: "ok", Usage: "usage_source=none cost=none", Sel: sprint.Sel{IDs: []string{b.ID}}}))
	rb := h.snap().Readers.Card(b.ID)
	assert.Equal(t, "ok", rb.F("verdict"))
	assert.Equal(t, cardcost.WhyNoTokens, cardcost.ParseUsage(rb.F(sprint.FieldUsage)).Unpriced)

	v := sprint.CardCostOf(h.snap().Work.Card("s1-1"))
	assert.Equal(t, before+2, v.Total.Records, "both reads are records of the card's complete cost")
	tc := sprint.StreamTierCosts(h.snap())["s1"]
	assert.Equal(t, "$1.00", tc.ReadCost)
	assert.Equal(t, 1, tc.ReadsNoTokens)
	h.clean("reads priced")
}

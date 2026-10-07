package sprint

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// TestEveryFinishedReadHasACostRecordAndStatsSumsIt is the cost a person can read
// (the owner, 2026-10-04: "we MUST track the complete cost"; 2026-10-06: fleet reads
// were not on the stats reads table). A finished read keeps a cost record, as a work
// attempt does: the route, the model, the tokens in and out, and the price from the
// route's sheet, or unpriced with the reason named. Stats sums that per reader (the
// charged total, the median per priced read, the unpriced count). A landed card's
// complete cost is those work attempts plus those reads, written at landing, and the
// stream's where record puts the reads in the total, in per landed, and on their own.
func TestEveryFinishedReadHasACostRecordAndStatsSumsIt(t *testing.T) {
	t.Parallel()
	w, reads := routedReads(t)
	a, b := reads[0], reads[1]
	// 500,000 in and 50,000 out on pro-a is $0.50 + $0.50
	w.must(Read(w.s, ReadReq{As: a.Row, Verdict: "ok", Usage: "input=500000 output=50000 model=opencode/other", Sel: Sel{IDs: []string{a.ID}}}))
	// a harness that reported no token: the verdict is kept, the record names why
	w.must(Read(w.s, ReadReq{As: b.Row, Verdict: "ok", Usage: "wall=3s usage_source=none", Sel: Sel{IDs: []string{b.ID}}}))

	v := CardCostOf(w.s.Work.Card("s1-1"))
	byReader := map[string]Consumer{}
	for _, c := range v.Consumers {
		if c.Kind == "read" {
			byReader[c.Who] = c
		}
	}
	require.Len(t, byReader, 2, "each finished read is a record on the primary")
	priced := byReader[a.Row]
	assert.Equal(t, "pro-a", priced.Usage.Route, "priced from the read card's route")
	assert.Equal(t, "opencode/other", priced.Model)
	assert.Equal(t, int64(500000), priced.Usage.Tokens.Input)
	assert.Equal(t, int64(50000), priced.Usage.Tokens.Output)
	assert.Equal(t, "1", priced.Usage.Predicted)
	bare := byReader[b.Row]
	assert.Equal(t, cardcost.WhyNoTokens, bare.Usage.Unpriced)
	assert.Empty(t, cmp.Or(bare.Usage.Actual, bare.Usage.Predicted))

	ps := Stats(w.s)
	got := map[string]ReaderStat{}
	for _, r := range ps.Reads {
		got[r.Reader] = r
	}
	require.Contains(t, got, a.Row)
	require.Contains(t, got, b.Row)
	assert.Equal(t, "$1.00", got[a.Row].Cost)
	assert.Equal(t, "$1.00", got[a.Row].CostMedian, "one priced read: the median is that read")
	assert.Equal(t, 0, got[a.Row].Unpriced)
	assert.Equal(t, "-", got[b.Row].Cost)
	assert.Equal(t, "-", got[b.Row].CostMedian)
	assert.Equal(t, 1, got[b.Row].Unpriced)

	// two priced reads of one reader: the total is their sum, the median the mean of
	// the two middle costs (the same rule as a stage's median)
	s := &Snapshot{Now: t0, Work: NewTable(Work), Fleet: NewTable(Fleet), Readers: NewTable(Readers)}
	s.Work.SetRows([]string{"s1"})
	p := &Card{ID: "p", Row: "s1", Col: Landed, Fields: map[string]string{"attempt": "1"}}
	put := func(take int, usd string) {
		c := Consumer{Kind: "read", Card: "p.r1.reader-a", Who: "reader-a", Take: take, Route: "pro-a", End: "ok", At: stamp(t0),
			Usage: cardcost.ParseUsage("predicted_usd=" + usd + " price_route=pro-a")}
		c.Key = c.Card + "#t" + itoa(take)
		p.Fields[FieldCostRecord+c.Key] = c.line()
	}
	put(1, "1")
	put(2, "3")
	s.Work.Put(p)
	var ra ReaderStat
	for _, r := range Stats(s).Reads {
		if r.Reader == "reader-a" {
			ra = r
		}
	}
	assert.Equal(t, "$4.00", ra.Cost)
	assert.Equal(t, "$2.00", ra.CostMedian, "the mean of $1 and $3, then up to the cent")
	assert.Equal(t, 0, ra.Unpriced)

	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	require.Equal(t, Landed, w.state("s1-1"))
	assert.Equal(t, "3", w.s.Work.Card("s1-1").F(FieldCost), "work $2 plus the priced read $1, written at landing")
	tc := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, "$3.00", tc.TotalCost, "the stream sum includes the read")
	assert.Equal(t, "$3.00", tc.PerLanded, "per landed is that complete cost over the one landed card")
	assert.Equal(t, "$2.00", tc.WorkCost)
	assert.Equal(t, "$1.00", tc.ReadCost)
	assert.Equal(t, "$1.00", tc.CostReads, "the reads on their own, the same dollars")
	assert.Equal(t, 1, tc.ReadsUnpriced)
}

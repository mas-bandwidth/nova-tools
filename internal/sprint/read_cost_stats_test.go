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

func TestALapsedBegunReadEndsWithItsCostRecord(t *testing.T) {
	t.Parallel()
	w, reads := routedReads(t)
	for _, rc := range reads {
		w.must(Read(w.s, ReadReq{As: rc.Row, Begin: true, Sel: Sel{IDs: []string{rc.ID}}}))
	}
	w.s.Now = w.s.Now.Add(DefaultReadLease + 1)
	w.must(RestartReads(w.s))
	v := CardCostOf(w.s.Work.Card("s1-1"))
	require.Len(t, v.Consumers, 3, "both begun runs ended, so both costs must survive retirement")
	for _, c := range v.Consumers {
		if c.Kind == "read" {
			assert.Equal(t, "no-tokens", c.Usage.Unpriced)
			assert.Equal(t, "pro-a", c.Route)
		}
	}
}

// The read-card timeout ends only started runs; an unstarted queue is not spend.
func TestReadCardRetirementPricesOnlyStartedRuns(t *testing.T) {
	t.Parallel()
	for _, col := range []string{Ready, Working} {
		t.Run(col, func(t *testing.T) {
			t.Parallel()
			w := readCardsWorld(t, 4, "m1", "m2", "m3")
			putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
			dealReads(t, w, nil)
			cards := readCardsOf(w, "s1-1")
			require.Len(t, cards, 2)
			for _, c := range cards {
				c.Col = col
				if col == Working {
					c.Fields["taken"] = stamp(w.s.Now)
					c.Fields[FieldUsage] = "input=500000 output=50000"
					c.Fields[FieldRoute] = "pro-a"
				}
				w.s.Fleet.Put(c)
			}
			w.s.Routes = []Route{pricedRoute}
			w.s.Now = w.s.Now.Add(ReadCardDeadline + w.s.DealtMax())
			var p Plan
			for _, changes := range readCardsTakeBack(w.s) {
				p.Units = append(p.Units, Unit{Key: "s1-1", Changes: changes})
			}
			w.must(p)
			costs := CardCostOf(w.s.Work.Card("s1-1"))
			if col == Ready {
				assert.Empty(t, costs.Consumers)
			} else {
				require.Len(t, costs.Consumers, 2)
				assert.Equal(t, "2", costs.Total.Charged)
				for _, c := range costs.Consumers {
					assert.Equal(t, "pro-a", c.Route)
					assert.Equal(t, "retired late", c.End)
				}
			}
		})
	}
}

func TestReadCostStatsKeepsRecentReadsWhenConsumerCardsAreGone(t *testing.T) {
	t.Parallel()
	w, reads := routedReads(t)
	for _, c := range reads {
		w.must(Read(w.s, ReadReq{As: c.Row, Verdict: "ok", Usage: "input=500000 output=50000", Sel: Sel{IDs: []string{c.ID}}}))
	}
	// Consumer tables may be tidied; the primary remains the cost truth.
	w.s.Readers = NewTable(Readers)
	w.s.Fleet = NewTable(Fleet)
	pr := w.s.Work.Card("s1-1")
	pr.Fields["admitted"] = stamp(w.s.Now.Add(-DefaultReadLease))
	since := w.s.Now
	stats := StatsSince(w.s, since)
	require.Len(t, stats.Reads, 2)
	for _, rd := range stats.Reads {
		assert.Equal(t, "$1.00", rd.Cost)
		assert.Equal(t, "$1.00", rd.CostMedian)
	}
	assert.Empty(t, StatsSince(w.s, since.Add(DefaultReadLease)).Reads)
}

func readCostsOf(pr *Card) []Consumer {
	var out []Consumer
	for _, c := range CardCostOf(pr).Consumers {
		if c.Kind == "read" {
			out = append(out, c)
		}
	}
	return out
}

func TestAskInsteadOfABegunReadKeepsItsCostAndAnUnbegunAskDoesNot(t *testing.T) {
	t.Parallel()
	t.Run("unbegun", func(t *testing.T) {
		t.Parallel()
		w, reads := routedReads(t)
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: reads[0].Row}))
		assert.Empty(t, readCostsOf(w.s.Work.Card("s1-1")))
	})
	t.Run("begun", func(t *testing.T) {
		t.Parallel()
		w, reads := routedReads(t)
		w.must(Read(w.s, ReadReq{As: reads[0].Row, Begin: true, Sel: Sel{IDs: []string{reads[0].ID}}}))
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: reads[0].Row}))
		got := readCostsOf(w.s.Work.Card("s1-1"))
		require.Len(t, got, 1)
		assert.Equal(t, reads[0].F("reader"), got[0].Who)
		assert.Equal(t, "pro-a", got[0].Route)
		assert.Equal(t, cardcost.WhyNoTokens, got[0].Usage.Unpriced)
		assert.Equal(t, "retired coordinator", got[0].End)
		assert.Empty(t, cmp.Or(got[0].Usage.Actual, got[0].Usage.Predicted))
	})
}

func TestAcceptAndReworkKeepABegunReadsCost(t *testing.T) {
	t.Parallel()
	t.Run("accept", func(t *testing.T) {
		t.Parallel()
		w, reads := routedReads(t)
		w.s.Work.SetProp(PropReadsNeeded, "1")
		w.must(Read(w.s, ReadReq{As: reads[0].Row, Verdict: "ok", Usage: "input=500000 output=50000", Sel: Sel{IDs: []string{reads[0].ID}}}))
		w.must(Read(w.s, ReadReq{As: reads[1].Row, Begin: true, Sel: Sel{IDs: []string{reads[1].ID}}}))
		w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		got := map[string]Consumer{}
		for _, c := range readCostsOf(w.s.Work.Card("s1-1")) {
			got[c.Who] = c
		}
		require.Len(t, got, 2)
		assert.Equal(t, "ok", got[reads[0].F("reader")].End)
		assert.Equal(t, "1", got[reads[0].F("reader")].Usage.Predicted)
		assert.Equal(t, "retired accept", got[reads[1].F("reader")].End)
		assert.Equal(t, cardcost.WhyNoTokens, got[reads[1].F("reader")].Usage.Unpriced)
		assert.Equal(t, "pro-a", got[reads[1].F("reader")].Route)
	})
	t.Run("rework", func(t *testing.T) {
		t.Parallel()
		w, reads := routedReads(t)
		w.must(Read(w.s, ReadReq{As: reads[0].Row, Begin: true, Sel: Sel{IDs: []string{reads[0].ID}}}))
		w.must(Read(w.s, ReadReq{As: reads[1].Row, Verdict: "broken", Finding: "internal/x.go:3 is wrong: change it",
			Usage: "input=500000 output=50000", Sel: Sel{IDs: []string{reads[1].ID}}}))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "change it"}))
		got := map[string]Consumer{}
		for _, c := range readCostsOf(w.s.Work.Card("s1-1")) {
			got[c.Who] = c
		}
		require.Len(t, got, 2)
		assert.Equal(t, "broken", got[reads[1].F("reader")].End)
		assert.Equal(t, "retired rework", got[reads[0].F("reader")].End)
		assert.Equal(t, cardcost.WhyNoTokens, got[reads[0].F("reader")].Usage.Unpriced)
		assert.Equal(t, "pro-a", got[reads[0].F("reader")].Route)
	})
}

func TestReturnReadsPricesABegunReadOnly(t *testing.T) {
	t.Parallel()
	w, reads := routedReads(t)
	w.must(Read(w.s, ReadReq{As: reads[0].Row, Begin: true, Sel: Sel{IDs: []string{reads[0].ID}}}))
	p, _ := returnReads(w.s, reads[0].Row, "coordinator")
	w.must(p)
	p, _ = returnReads(w.s, reads[1].Row, "coordinator")
	w.must(p)
	got := readCostsOf(w.s.Work.Card("s1-1"))
	require.Len(t, got, 1)
	assert.Equal(t, reads[0].F("reader"), got[0].Who)
	assert.Equal(t, "retired hold", got[0].End)
	assert.Equal(t, cardcost.WhyNoTokens, got[0].Usage.Unpriced)
	assert.Equal(t, "pro-a", got[0].Route)
}

func TestAMemberDownPricesAWorkingReadAndNotAReadyOne(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2", "m3")
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
	dealReads(t, w, nil)
	cards := readCardsOf(w, "s1-1")
	require.Len(t, cards, 2)
	require.NotEqual(t, cards[0].Row, cards[1].Row)
	working := cards[0]
	working.Col = Working
	working.Fields["taken"] = stamp(w.s.Now)
	working.Fields[FieldUsage] = "input=500000 output=50000"
	working.Fields[FieldRoute] = "pro-a"
	w.s.Fleet.Put(working)
	w.s.Routes = []Route{pricedRoute}
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: working.Row}))
	got := readCostsOf(w.s.Work.Card("s1-1"))
	require.Len(t, got, 1)
	assert.Equal(t, "1", got[0].Usage.Predicted)
	assert.Equal(t, "pro-a", got[0].Route)
	assert.Equal(t, "retired away", got[0].End)
	ready := cards[1]
	require.Equal(t, Ready, ready.Col)
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: ready.Row}))
	assert.Len(t, readCostsOf(w.s.Work.Card("s1-1")), 1, "a ready read has not started")
}

func TestFriendCloseWithoutUsageIsUnpricedAndAReturnAddsNone(t *testing.T) {
	t.Parallel()
	t.Run("close", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.s.Work.SetRows([]string{"s1"})
		w.s.Fleet.SetRows([]string{"friend.amy"})
		w.s.Routes = []Route{pricedRoute}
		pr := &Card{ID: "s1-1", Row: "s1", Col: Review, Fields: map[string]string{"attempt": "1", "kind": "primary"}}
		w.s.Work.Put(pr)
		rc := &Card{ID: "s1-1.r1.amy", Row: "friend.amy", Col: Working, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "reader": "amy", "attempt": "1", "asked": stamp(t0), FieldRoute: "pro-a"}}
		w.s.Fleet.Put(rc)
		w.must(Plan{Units: []Unit{friendReadCloseUnit(w.s, "amy", pr, rc, "ok", "", "")}})
		got := readCostsOf(w.s.Work.Card("s1-1"))
		require.Len(t, got, 1)
		assert.Equal(t, "amy", got[0].Who)
		assert.Equal(t, "ok", got[0].End)
		assert.Equal(t, "pro-a", got[0].Route)
		assert.Equal(t, cardcost.WhyNoTokens, got[0].Usage.Unpriced)
		assert.Empty(t, w.s.Fleet.Card(rc.ID).F(FieldUsage), "no usage was reported, so the card keeps none")
	})
	t.Run("return", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.s.Work.SetRows([]string{"s1"})
		w.s.Fleet.SetRows([]string{"m1"})
		w.s.Work.Put(&Card{ID: "s1-1", Row: "s1", Col: Review, Rev: 1, Fields: map[string]string{"attempt": "1", "kind": "primary"}})
		w.s.Fleet.Put(&Card{ID: "s1-1.r1.m1", Row: "m1", Col: Working, Rev: 1, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "reader": "m1", "attempt": "1", "stream": "s1", "taken": stamp(t0), FieldRoute: "pro-a"}})
		w.must(readCardVerb(w.s, ReadReq{As: "m1", Return: true, Reason: "no", Sel: Sel{IDs: []string{"s1-1.r1.m1"}}}, "m1", "m1"))
		assert.Empty(t, readCostsOf(w.s.Work.Card("s1-1")))
	})
}

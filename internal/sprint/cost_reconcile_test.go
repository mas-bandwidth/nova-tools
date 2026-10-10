package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// reconcileWorld is a world with an openrouter route and one primary carrying records of
// $10.00 on the day of t0 (a take priced by the harness, a read priced at the route's prices)
// and $40.00 the day before, and an opencode take of $5.00 on the day of t0.
func reconcileWorld(t *testing.T) *world {
	w := newWorld(t)
	w.s.Routes = []Route{{Name: "flash-or", Provider: "openrouter", Model: "m"}, {Name: "flash-oc", Provider: "opencode", Model: "m"}}
	w.s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{}}
	today, yesterday := stamp(w.s.Now.Add(-time.Hour)), stamp(w.s.Now.Add(-24*time.Hour))
	book(pr,
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g1", Route: "flash-or", End: "no result", At: today, Usage: cardcost.ParseUsage("input=100 actual_usd=6 actual_by=harness")},
		Consumer{Kind: "read", Card: "s1-1.r1", Key: "s1-1.r1#v", Model: "openrouter/m", End: "ok", At: today, Usage: cardcost.ParseUsage("input=50 predicted_usd=4")},
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g0", Route: "flash-or", End: "failed", At: yesterday, Usage: cardcost.ParseUsage("input=100 actual_usd=40 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#oc", Route: "flash-oc", End: "ok", At: today, Usage: cardcost.ParseUsage("input=100 actual_usd=5 actual_by=harness")},
	)
	w.s.Work.Put(pr)
	return w
}

func openrouterRead(w *world, used float64) CostReconcileReq {
	return CostReconcileReq{Reads: []UsageRead{{Provider: "openrouter", Known: true, Day: w.s.Now.UTC().Format(time.DateOnly), Used: used}}}
}

func gapJudgments(w *world) int {
	n := 0
	for _, o := range w.s.Open {
		if o.Note.Type == NCostGap {
			n++
		}
	}
	return n
}

// The sprint's records of a provider on a day are every take and read of that provider that
// ended that day, whatever its end, at its charged figure: never another day's, never another
// provider's.
func TestTheRecordsOfADayAreThatDaysOnly(t *testing.T) {
	t.Parallel()
	w := reconcileWorld(t)
	day := w.s.Now.UTC().Format(time.DateOnly)
	assert.InDelta(t, 10.0, InternalSpendOn(w.s, "openrouter", day), 1e-9)
	assert.InDelta(t, 40.0, InternalSpendOn(w.s, "openrouter", w.s.Now.Add(-24*time.Hour).UTC().Format(time.DateOnly)), 1e-9)
	assert.InDelta(t, 5.0, InternalSpendOn(w.s, "opencode", day), 1e-9)
}

// A gap of 10% between the provider's count of today and the sprint's records of today opens
// ONE judgment on the provider (fake provider read, the world's clock); a second read past the
// bound an hour later opens none, a read back within it closes it, and a gap past the bound
// again opens one anew.
func TestAReconciliationRaisesOneJudgmentOnATenPercentGap(t *testing.T) {
	t.Parallel()
	w := reconcileWorld(t)
	p := w.must(CostReconcile(w.s, openrouterRead(w, 100.0/9))) // $11.11 against $10.00: 10%
	require.Len(t, p.Notes, 1)
	assert.Equal(t, NCostGap, p.Notes[0].Type)
	assert.Equal(t, Judgment, p.Notes[0].Kind)
	assert.Equal(t, ProviderSubject("openrouter"), p.Notes[0].Stream)
	assert.Contains(t, p.Notes[0].What, "provider openrouter counted $11.12 on 2030-01-02 and the sprint's cost records of it hold $10.00")
	assert.Equal(t, []string{"ack", "wait"}, p.Notes[0].Decisions)
	rec, ok := CostReconcileOf(w.s.Fleet, "openrouter")
	require.True(t, ok)
	assert.InDelta(t, 10.0, rec.Internal, 1e-9, "today's records, never yesterday's $40")
	assert.InDelta(t, 0.1, rec.Share, 1e-9)

	w.tick(CostReconcileEvery)
	p = w.must(CostReconcile(w.s, openrouterRead(w, 12)))
	assert.Empty(t, p.Notes, "no second judgment while the one is open")
	assert.Equal(t, 1, gapJudgments(w))

	w.tick(CostReconcileEvery)
	w.must(CostReconcile(w.s, openrouterRead(w, 10.2)))
	assert.Zero(t, gapJudgments(w), "a read within 5% closes it")

	w.tick(CostReconcileEvery)
	w.must(CostReconcile(w.s, openrouterRead(w, 12)))
	assert.Equal(t, 1, gapJudgments(w), "past the bound again: one anew")
}

// The bound: a gap within 5%, or under CostGapFloor dollars, opens nothing; an unknown
// read writes why and opens nothing, keeping the days it had.
func TestAReconciliationWithinTheBoundOrUnknownOpensNothing(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		read UsageRead
	}{
		{"within 5%", UsageRead{Provider: "openrouter", Known: true, Used: 10.2}},
		{"under the floor", UsageRead{Provider: "opencode", Known: true, Used: 5.9}},
		{"unknown", UsageRead{Provider: "openrouter", Note: "OPENROUTER_API_KEY is not in the run loop's environment"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := reconcileWorld(t)
			c.read.Day = w.s.Now.UTC().Format(time.DateOnly)
			p := w.must(CostReconcile(w.s, CostReconcileReq{Reads: []UsageRead{c.read}}))
			assert.Empty(t, p.Notes)
			rec, ok := CostReconcileOf(w.s.Fleet, c.read.Provider)
			require.True(t, ok)
			assert.Equal(t, c.read.Known, rec.Known)
			assert.Equal(t, c.read.Note, rec.Note)
		})
	}
}

// What is unreconciled is, over the days since the epoch began, each day's last provider
// figure beyond the sprint's records of it: a day the records exceed adds nothing, and a day
// before the epoch is not the sprint's.
func TestUnreconciledSpendIsEachDaysLastGapSinceTheEpoch(t *testing.T) {
	t.Parallel()
	w := reconcileWorld(t)
	w.s.Cleared = w.s.Now.Add(-20 * time.Hour) // the day before t0
	before := w.s.Now
	w.s.Now = before.Add(-48 * time.Hour) // a day before the epoch: $3 over records of $0
	w.must(CostReconcile(w.s, openrouterRead(w, 3)))
	w.s.Now = before.Add(-24 * time.Hour) // yesterday: $45 over $40
	w.must(CostReconcile(w.s, openrouterRead(w, 45)))
	w.s.Now = before
	w.must(CostReconcile(w.s, openrouterRead(w, 11))) // today, read twice: the last read stands
	w.tick(time.Minute)
	w.must(CostReconcile(w.s, openrouterRead(w, 12)))
	w.must(CostReconcile(w.s, CostReconcileReq{Reads: []UsageRead{{Provider: "opencode", Known: true, Day: w.s.Now.UTC().Format(time.DateOnly), Used: 4}}}))
	assert.InDelta(t, 5.0+2.0, UnreconciledSpend(w.s), 1e-9, "yesterday's $5 and today's $2; opencode's records exceed its count")

	tc := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, "$7.00", tc.Unreconciled, "the sprint's, on the stream's record")
	assert.Equal(t, "$55.00", tc.TotalCost, "every record of the card, any day, any provider")
}

// A stream's total is every take and read of every card in any column, landed or not, and a
// record with no cost is counted as unpriced, never as a zero.
func TestAStreamsTotalIsEveryRecordOfEveryCard(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Fleet: NewTable(Fleet), Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1"})
	landed := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{}}
	book(landed,
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g1", End: "failed", At: stamp(t0), Usage: cardcost.ParseUsage("input=1 actual_usd=2 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g2", End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage("input=1 actual_usd=3 actual_by=harness")},
	)
	landed.Fields[FieldCost] = "5"
	working := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(working,
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g1", End: "no result", At: stamp(t0), Usage: cardcost.ParseUsage("input=10 predicted_usd=3.5")},
		Consumer{Kind: "work", Card: "s1-2", Key: "s1-2#g2", End: "no result", At: stamp(t0), Usage: cardcost.ParseUsage("unpriced=no-tokens")},
	)
	s.Work.Put(landed)
	s.Work.Put(working)
	tc := StreamTierCosts(s)["s1"]
	assert.Equal(t, "$8.50", tc.TotalCost)
	assert.Equal(t, 1, tc.UnpricedRuns)
	assert.Equal(t, "$5.00", tc.PerLanded)
	assert.Empty(t, tc.Unreconciled)
}

// The judgment names the read share of the gap (the owner, 2026-10-05, before funding a
// provider for reads: track what readers spend): the reads among the day's records, their
// share, and the gap spread as the records are; the record keeps the reads beside the total.
func TestTheReconcileJudgmentNamesTheReadShareOfTheGap(t *testing.T) {
	t.Parallel()
	w := reconcileWorld(t)
	day := w.s.Now.UTC().Format(time.DateOnly)
	assert.InDelta(t, 4.0, InternalReadSpendOn(w.s, "openrouter", day), 1e-9, "today's one read, never a take")
	assert.InDelta(t, 0.0, InternalReadSpendOn(w.s, "opencode", day), 1e-9)

	p := w.must(CostReconcile(w.s, openrouterRead(w, 20))) // $20 against $10: a gap of $10
	require.Len(t, p.Notes, 1)
	assert.Contains(t, p.Notes[0].What, "reads are $4.00 of the records (40.0%, work $6.00), so spread as the records are, about $4.00 of the gap is reads")
	assert.Contains(t, p.Notes[0].What, "a route's prices are under its provider's list")
	rec, ok := CostReconcileOf(w.s.Fleet, "openrouter")
	require.True(t, ok)
	assert.InDelta(t, 4.0, rec.InternalReads, 1e-9)
	require.Len(t, p.Units, 1)
	assert.Contains(t, p.Units[0].Moved, "records=$10.00 reads=$4.00 gap=50.0%")

	// a day with no record names no read share
	assert.Equal(t, "the records hold nothing of the day, so no part of the gap is set against reads",
		readShareOfGap(CostReconcileRecord{Used: 3}))
}

// InternalSpendOn is the sprint's records of a provider on a UTC day (2006-01-02): every
// consumer record on every primary of the work table that ended that day, at its charged
// figure (the harness's cost, else the predicted one at the route's prices).
func InternalSpendOn(s *Snapshot, provider, day string) float64 {
	all, _ := internalSpendSplit(s, provider, day)
	return all
}

// InternalReadSpendOn is the reads among InternalSpendOn's records: what the reads of that
// provider and day cost, the read share of the records the provider's count is set beside.
func InternalReadSpendOn(s *Snapshot, provider, day string) float64 {
	_, reads := internalSpendSplit(s, provider, day)
	return reads
}

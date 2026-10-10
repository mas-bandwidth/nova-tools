package sprint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// Stats reset on the twin store (the owner, 2026-10-09: "Clear the cost per-card right now.
// Clear the per-tier costs. Clear the total cost.", "Clear the done and the ok% for all
// friends now.", "everything that I described should happen when you go reset"). Nothing
// moves; every figure the mark covers counts from it, never below zero.

// resetView is what where shows of the sprint, read as where reads it: the tick's where
// record's facts, and the work and fleet tables' shapes with the reset's mark applied as
// where applies it (FleetSince, LandedSince).
type resetView struct {
	facts store.WhereFacts
	work  ntable.Table
	fleet ntable.Table
}

func (r *conflictRig) resetView() resetView {
	r.t.Helper()
	r.tick()
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(r.t, err)
	shapes, err := r.m.Shapes(r.ctx, []string{pinned.Names.Table(sprint.Work), pinned.Names.Table(sprint.Fleet)})
	require.NoError(r.t, err)
	facts, err := pinned.WhereFacts(r.ctx, shapes[0].Revision)
	require.NoError(r.t, err)
	return resetView{facts: facts, work: shapes[0], fleet: facts.Reset.FleetSince(shapes[1])}
}

// cell is the row's cell of the column as where prints it.
func cell(t ntable.Table, row, col string) string {
	i, j := t.Row(row), t.Column(col)
	if i < 0 || j < 0 {
		return "no " + row + "/" + col
	}
	return ntable.CellText(t.Columns, t.Rows[i], j)
}

func TestAStatsResetCountsEveryFigureFromItsMarkAndMovesNothing(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.landOnM1(3)
	before := r.resetView()
	tc := before.facts.Streams["s1"]
	require.NotEmpty(t, tc.TotalCost, "s1's spend before the reset")
	require.NotEmpty(t, tc.CostByTier)
	require.Equal(t, "3", cell(before.fleet, "m1", sprint.Done), "three done on m1 before the reset")
	require.Nil(t, before.facts.Reset)
	where := places(r.snap())

	// a dry run writes nothing
	dry, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now", DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, sprint.ResetRow{OK: 3}, dry.Mark.Rows["m1"])
	m, err := r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	assert.Nil(t, m, "a dry run writes no mark")

	res, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now"})
	require.NoError(t, err)
	assert.Nil(t, res.Replaced)
	mark := res.Mark
	assert.Equal(t, r.st.Now().UTC(), mark.At)
	assert.Equal(t, "coordinator", mark.By)
	assert.Equal(t, sprint.ResetRow{OK: 3}, mark.Rows["m1"])
	assert.Equal(t, 3, mark.Streams["s1"].Landed)
	assert.Equal(t, tc.TotalCost, mark.Streams["s1"].TotalCost, "the mark holds the figures the where view showed")
	assert.Equal(t, tc.CostByTier, mark.Streams["s1"].CostByTier)
	assert.Equal(t, tc.TotalCost, mark.TotalCost, "one stream: the epoch's total is s1's")
	assert.Equal(t, where, places(r.snap()), "a reset moves nothing: no card, no stream")
	r.clean("reset")

	// nothing since the mark: zeros, and "-" wherever a figure has nothing to show
	now := r.resetView()
	require.NotNil(t, now.facts.Reset)
	s1 := now.facts.Streams["s1"]
	assert.Empty(t, s1.TotalCost, "the total cost counts from the mark: nothing (the tile's $0.00)")
	assert.Empty(t, s1.WorkCost)
	assert.Empty(t, s1.ReadCost)
	assert.Empty(t, s1.CostByTier, "no tier has spent since the mark: the pie is empty")
	assert.Equal(t, "-", s1.PerLanded)
	assert.Equal(t, "-", cell(now.work, "s1", sprint.Cost), "the stream's cost cell")
	assert.Equal(t, "0", cell(now.fleet, "m1", sprint.Done), "done counts from the mark")
	assert.Equal(t, int64(0), now.facts.Reset.LandedSince(now.work, nil), "no card landed since: the per card is blank")
	assert.Equal(t, "3", cell(now.work, "s1", sprint.Landed), "the work table's own counts are the epoch's")
	since, err := r.st.StatsSince(r.ctx, "")
	require.NoError(t, err)
	assert.Equal(t, mark.At, since, "stats counts from the mark")
	s, err := r.st.Load(r.ctx, []string{sprint.Work, sprint.Fleet, sprint.Readers}, sprint.StatsRecords)
	require.NoError(t, err)
	assert.Zero(t, sprint.StatsSince(s, since).Primaries)
	assert.Equal(t, 3, sprint.Stats(s).Primaries, "the epoch's numbers are all still there")

	// one card landed since: done 1, its cost the total and the per card, its tier alone
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.mu.Lock()
	r.now = r.now.Add(time.Minute)
	r.mu.Unlock()
	r.beat()
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.landWithCost("s1", "s1-4")
	r.must(store.DrainStep())
	require.Equal(t, sprint.Landed, r.snap().StateOf("s1-4"))
	card := r.snap().Work.Card("s1-4")
	worker := r.snap().Fleet.Card(card.F("work")).Row // the machine the tick dealt it to
	atMark := mark.Rows[worker]
	spent := sprint.CardCostOf(card).Total.Charged
	require.NotEmpty(t, spent)
	one := r.resetView()
	s1 = one.facts.Streams["s1"]
	want := cents(t, spent)
	assert.Equal(t, want, s1.TotalCost, "the total is the one card's whole spend")
	assert.Len(t, s1.CostByTier, 1, "its tier alone")
	for _, v := range s1.CostByTier {
		assert.Equal(t, want, v)
	}
	landedCost := card.F(sprint.FieldCost)
	assert.Equal(t, cents(t, landedCost), s1.PerLanded, "per landed is the one card's")
	assert.Equal(t, sprint.MoneyText(landedCost), cell(one.work, "s1", sprint.Cost))
	assert.Equal(t, int64(1), one.facts.Reset.LandedSince(one.work, nil), "per card is over the one card")
	assert.Equal(t, "1", cell(one.fleet, worker, sprint.Done), "done counts the one card on %s", worker)
	assert.Equal(t, "100.0%", cell(one.fleet, worker, sprint.OkPct))

	// a second reset replaces the first: its mark holds the card, and the figures are zero again
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	again, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "again"})
	require.NoError(t, err)
	require.NotNil(t, again.Replaced)
	assert.Equal(t, "count from now", again.Replaced.Reason)
	got, err := r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "again", got.Reason)
	assert.Equal(t, sprint.ResetRow{OK: atMark.OK + 1, Failed: atMark.Failed}, got.Rows[worker])
	zero := r.resetView()
	assert.Empty(t, zero.facts.Streams["s1"].TotalCost)
	assert.Equal(t, "0", cell(zero.fleet, worker, sprint.Done))
	assert.Equal(t, "-", cell(zero.work, "s1", sprint.Cost))

	// a tidy after the reset keeps the mark as it found it
	r.mu.Lock()
	r.now = r.now.Add(2 * time.Minute)
	r.mu.Unlock()
	_, err = r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyRoutes}, Reason: "routes"})
	require.NoError(t, err)
	got, err = r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "again", got.Reason)
}

// cents is a recorded exact amount as the where view shows money, rounded up to the cent.
func cents(t *testing.T, usd string) string {
	t.Helper()
	sum, ok := cardcost.Sum(usd)
	require.True(t, ok, usd)
	r, ok2 := new(big.Rat).SetString(sum)
	require.True(t, ok2, sum)
	return cardcost.Cents(r)
}

// A row the mark does not know reads as before; a known row never shows below zero; an
// unread cell stays unread.
func TestTheFleetSinceAMarkLeavesARowItDoesNotKnow(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,ok,failed")
	require.NoError(t, err)
	row := func(key string, ok, failed int64) ntable.Row {
		return ntable.Row{Key: key, Cells: []ntable.Cell{{}, {}, {Count: ok}, {Count: failed}}}
	}
	t0 := ntable.Table{Name: sprint.Fleet, Columns: cols, Rows: []ntable.Row{row("m1", 10, 2), row("m2", 5, 5), row("friend.stella", 1, 0)}}
	m := &sprint.ResetMark{Rows: map[string]sprint.ResetRow{"m1": {OK: 8, Failed: 1}, "friend.stella": {OK: 3}}}
	got := m.FleetSince(t0)
	assert.Equal(t, "3", cell(got, "m1", sprint.Done))
	assert.Equal(t, "66.7%", cell(got, "m1", sprint.OkPct), "ok% over the cards since the mark: 2 of 3")
	assert.Equal(t, "10", cell(got, "m2", sprint.Done), "a row the mark did not know reads as before")
	assert.Equal(t, "50.0%", cell(got, "m2", sprint.OkPct))
	assert.Equal(t, "0", cell(got, "friend.stella", sprint.Done), "never below zero")
	assert.Equal(t, "12", cell(t0, "m1", sprint.Done), "the table read is not changed")
	var none *sprint.ResetMark
	assert.Equal(t, t0, none.FleetSince(t0), "no mark: the table as it is")
}

// The money figures less the mark's: dollars and cents, nothing for nothing or below, the
// figure as it was when the mark names none; per tier, a tier with nothing since left out.
func TestTierCostsSinceAMark(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$1.50", sprint.MoneyLess("$2.75", "$1.25"))
	assert.Equal(t, "", sprint.MoneyLess("$1.25", "$1.25"))
	assert.Equal(t, "", sprint.MoneyLess("$1.00", "$1.25"), "never below zero")
	assert.Equal(t, "$1.25", sprint.MoneyLess("$1.25", ""), "a figure the mark does not name reads as before")
	assert.Equal(t, "", sprint.MoneyLess("", "$1.25"))
	tc := sprint.TierCosts{TotalCost: "$3.00", WorkCost: "$2.00", ReadCost: "$1.00", PerLanded: "$1.00",
		CostByTier: map[string]string{"flash": "$1.00", "pro": "$2.00"}, Tiers: map[string]int{"flash": 1, "pro": 2}}
	got := sprint.TierCostsSince(tc, sprint.ResetStream{TotalCost: "$1.00", WorkCost: "$0.50", ReadCost: "$0.50", CostByTier: map[string]string{"flash": "$1.00"}})
	assert.Equal(t, "$2.00", got.TotalCost)
	assert.Equal(t, "$1.50", got.WorkCost)
	assert.Equal(t, "$0.50", got.ReadCost)
	assert.Equal(t, map[string]string{"pro": "$2.00"}, got.CostByTier, "flash spent nothing since: left out")
	assert.Equal(t, tc.Tiers, got.Tiers, "the cards by tier are not covered: as before")
	assert.Equal(t, "$1.00", got.PerLanded, "per landed is counted from the base by the tick")
	assert.Equal(t, map[string]string{"flash": "$1.00", "pro": "$2.00"}, tc.CostByTier, "the figures read are not changed")
	assert.True(t, strings.HasPrefix(got.TotalCost, "$"))
}

// A tidy of the fleet after a reset: later wins and the reset is kept. The tidy takes the
// history off m1's done cells and rebases the mark's counters by what it took
// (sprint.ResetMark.Rebase), so m1's done since the mark is what it was (0), and one card
// landed after both shows done 1, where the mark's old counter would have clamped it to 0.
func TestAResetThenATidyOfTheFleetThenOneCardShowsDoneOne(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.landOnM1(13)
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.mu.Lock()
	r.now = r.now.Add(time.Hour + 500*time.Millisecond)
	r.mu.Unlock()
	reset, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now"})
	require.NoError(t, err)
	require.Equal(t, sprint.ResetRow{OK: 13}, reset.Mark.Rows["m1"])
	r.mu.Lock()
	r.now = r.now.Add(2 * time.Minute)
	r.mu.Unlock()
	tidy, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "history"})
	require.NoError(t, err)
	require.Equal(t, 3, tidy.Moved, "the three oldest: the row's newest ten stay")
	m, err := r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	require.NotNil(t, m, "the tidy keeps the reset")
	assert.Equal(t, sprint.ResetRow{OK: 10}, m.Rows["m1"], "rebased by the three cards the tidy took off")
	assert.Equal(t, reset.Mark.At, m.At)
	assert.Equal(t, "0", cell(r.resetView().fleet, "m1", sprint.Done), "nothing since the mark")

	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.beat()
	r.must(store.FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	r.landWithCost("s1", "s1-14")
	r.must(store.DrainStep())
	require.Equal(t, sprint.Landed, r.snap().StateOf("s1-14"))
	worker := r.snap().Fleet.Card(r.snap().Work.Card("s1-14").F("work")).Row
	require.Equal(t, "m1", worker, "m2 is down: the card is m1's")
	v := r.resetView()
	assert.Equal(t, "1", cell(v.fleet, "m1", sprint.Done), "one card since the reset, the tidy after it notwithstanding")
	assert.Equal(t, "100.0%", cell(v.fleet, "m1", sprint.OkPct))
}

// The tick's read of the stats record and its write of the where record are two steps: a
// reset between them (it moves no table) must not leave a where record counted without its
// mark. The record carries the stamp of the stats record it was counted from, and where
// takes none whose stamp is not the one in force; the next tick counts it again, idle or not.
func TestAWhereRecordCountedBeforeAResetIsNeverTaken(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.landOnM1(3)
	before := r.resetView()
	spent := before.facts.Streams["s1"].TotalCost
	require.NotEmpty(t, spent)
	_, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now"})
	require.NoError(t, err)
	now := r.resetView()
	require.Empty(t, now.facts.Streams["s1"].TotalCost, "counted from the mark")

	// the tick that read the stats record before the reset writes its record after it, at the
	// work table's revision as it stands (the reset wrote no table): its spend is the epoch's
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(t, err)
	raw, ok, err := r.m.GetKey(r.ctx, "where")
	require.NoError(t, err)
	require.True(t, ok)
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &rec))
	require.NotEmpty(t, rec["stats"], "the record names the stats record it was counted from")
	delete(rec, "stats") // counted before the reset: no tidy, no mark
	rec["streams"].(map[string]any)["s1"].(map[string]any)["total_cost"] = spent
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, r.m.SetKey(r.ctx, "where", string(b)))
	shapes, err := r.m.Shapes(r.ctx, []string{pinned.Names.Table(sprint.Work)})
	require.NoError(t, err)
	require.Equal(t, shapes[0].Revision, uint64(rec["rev"].(float64)), "the stale record is at the revision where reads")
	facts, err := pinned.WhereFacts(r.ctx, shapes[0].Revision)
	require.NoError(t, err)
	assert.Nil(t, facts.Streams, "a record counted from another stats record is not taken, though its revision is current")

	// the next tick counts it again, from the mark
	again := r.resetView()
	assert.Empty(t, again.facts.Streams["s1"].TotalCost)
	assert.NotNil(t, again.facts.Streams)
}

// onSwap is the twin store with another writer that acts just before the at-th
// compare-and-set of the stats record (store.updateStats, ResetStats): what the act does
// lands between that writer's read and its write.
type onSwap struct {
	*store.Mem
	n   *int
	at  int
	act func(ctx context.Context) error
	err *error
}

func (o onSwap) AtEpoch(epoch uint64, old bool) store.Backend {
	return onSwap{Mem: o.Mem.AtEpoch(epoch, old).(*store.Mem), n: o.n, at: o.at, act: o.act, err: o.err}
}

func (o onSwap) SwapKey(ctx context.Context, name, was string, had bool, value string) (bool, error) {
	if name == "stats" {
		*o.n++
		if *o.n == o.at {
			*o.err = o.act(ctx)
		}
	}
	return o.Mem.SwapKey(ctx, name, was, had, value)
}

// swapping is a store on the rig's twin whose at-th write of the stats record runs act first.
func (r *conflictRig) swapping(at int, act func(ctx context.Context) error) (*store.Store, *int, *error) {
	n, err := 0, error(nil)
	return &store.Store{B: onSwap{Mem: r.m, n: &n, at: at, act: act, err: &err}, Names: r.st.Names, Actor: "coordinator",
		Now: r.st.Now, NewID: r.st.NewID, Sleep: r.st.Sleep}, &n, &err
}

// thirteenOnM1AnHourAgo is the rig with thirteen s1 cards finished on m1 and an hour of
// RUNNING since: a tidy of the fleet takes the three oldest off (the newest ten stay).
func thirteenOnM1AnHourAgo(t *testing.T) *conflictRig {
	r := newConflictRig(t)
	r.landOnM1(13)
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.mu.Lock()
	r.now = r.now.Add(time.Hour + 500*time.Millisecond)
	r.mu.Unlock()
	return r
}

// (a) A tidy that moves cards between a reset's read and its write: the reset's write finds
// the stats record changed and counts the mark again, from the fleet rows after the tidy;
// the mark it would have written (13 on m1) would sit above the row (10) and clamp a card
// after both to done 0.
func TestATidyBetweenAResetsReadAndWriteHasTheMarkCountedAgain(t *testing.T) {
	t.Parallel()
	r := thirteenOnM1AnHourAgo(t)
	var tidied store.TidyResult
	resetter, writes, actErr := r.swapping(1, func(ctx context.Context) error {
		var err error
		tidied, err = r.st.TidyStats(ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "history"})
		return err
	})
	res, err := resetter.ResetStats(r.ctx, store.ResetReq{Reason: "count from now"})
	require.NoError(t, err)
	require.NoError(t, *actErr)
	require.Empty(t, res.Refused)
	require.Equal(t, 3, tidied.Moved, "the tidy ran between the reset's read and its write")
	assert.Equal(t, 2, *writes, "the first write found the record changed; the second, counted again, wrote")
	assert.Equal(t, sprint.ResetRow{OK: 10}, res.Mark.Rows["m1"], "counted from the row after the tidy")
	m, err := r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, sprint.ResetRow{OK: 10}, m.Rows["m1"])
	assert.Equal(t, "0", cell(r.resetView().fleet, "m1", sprint.Done))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.beat()
	r.must(store.FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	r.landWithCost("s1", "s1-14")
	r.must(store.DrainStep())
	assert.Equal(t, "1", cell(r.resetView().fleet, "m1", sprint.Done), "one card since the reset")
}

// (b) A reset between a tidy's move and its last write: refused while the tidy is in flight,
// and the tidy rebases the mark it saw when it began (13 less the three it took off, all
// finished before it). Past a stale marker (a tidy whose process died) a reset goes on and
// counts its mark after the move; the tidy then leaves that mark alone, never taking the
// three off twice.
func TestAResetBetweenATidysMoveAndWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		later time.Duration // the resetter's clock past the tidy's
	}{{"in flight", 0}, {"past a stale marker", store.TidyStale + time.Minute}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := thirteenOnM1AnHourAgo(t)
			first, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "before the tidy"})
			require.NoError(t, err)
			require.Equal(t, sprint.ResetRow{OK: 13}, first.Mark.Rows["m1"])
			r.mu.Lock()
			r.now = r.now.Add(2 * time.Minute)
			r.mu.Unlock()
			var during store.ResetResult
			tidier, _, actErr := r.swapping(2, func(ctx context.Context) error { // 1: the marker; 2: the tidy's end
				late := &store.Store{B: r.m, Names: r.st.Names, Actor: "coordinator", NewID: r.st.NewID, Sleep: r.st.Sleep,
					Now: func() time.Time { return r.st.Now().Add(tc.later) }}
				var err error
				during, err = late.ResetStats(ctx, store.ResetReq{Reason: "during the tidy"})
				return err
			})
			tidied, err := tidier.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "history"})
			require.NoError(t, err)
			require.NoError(t, *actErr)
			require.Equal(t, 3, tidied.Moved)
			m, err := r.st.StatsReset(r.ctx)
			require.NoError(t, err)
			require.NotNil(t, m)
			rec, err := r.st.StatsTidied(r.ctx)
			require.NoError(t, err)
			assert.Nil(t, rec.Tidying, "the tidy's end clears its marker")
			if tc.later == 0 {
				assert.Contains(t, during.Refused, "a stats tidy is in flight", "refused while the tidy stands")
				assert.Equal(t, "before the tidy", m.Reason)
				assert.Equal(t, sprint.ResetRow{OK: 10}, m.Rows["m1"], "the mark the tidy saw, rebased by the three it took off")
			} else {
				require.Empty(t, during.Refused, "past a stale marker the reset goes on")
				assert.Equal(t, "during the tidy", m.Reason)
				assert.Equal(t, sprint.ResetRow{OK: 10}, m.Rows["m1"], "counted after the move, and not rebased again (7)")
			}
			assert.Equal(t, "0", cell(r.resetView().fleet, "m1", sprint.Done))
		})
	}
}

// A tidy does not take the oldest cards first: a card whose primary has not landed stays
// whatever its age (TidyKept). Here s2-1's work card finished before the reset and stays; the
// tidy takes off three s1 cards that finished after it. The mark is rebased by the moved
// cards that finished before it alone (none), so done is the cards since the mark still on
// the row: 13 since the reset, three taken off by the tidy, 10. A rebase by count (one per
// moved card) would lower the mark to 0 and count s2-1's card from before the reset: 11.
func TestATidyRebasesTheMarkByTheCardsFinishedBeforeIt(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 1, Brief: "c: the work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.toMerging("s2-1")
	kept := r.snap().Work.Card("s2-1").F("work")
	require.Equal(t, "m1", r.snap().Fleet.Card(kept).Row)
	r.mu.Lock()
	r.now = r.now.Add(time.Minute)
	r.mu.Unlock()
	reset, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now"})
	require.NoError(t, err)
	require.Equal(t, sprint.ResetRow{OK: 1}, reset.Mark.Rows["m1"], "s2-1's work card, finished before the reset")
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 10, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	for i := 1; i <= 13; i++ {
		r.mu.Lock()
		r.now = r.now.Add(time.Minute)
		r.mu.Unlock()
		r.beat()
		id := fmt.Sprintf("s1-%d", i)
		r.landWithCost("s1", id)
		r.must(store.DrainStep())
		require.Equal(t, sprint.Landed, r.snap().StateOf(id), "%s landed", id)
	}
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.mu.Lock()
	r.now = r.now.Add(time.Hour + 500*time.Millisecond)
	r.mu.Unlock()
	require.Equal(t, "13", cell(r.resetView().fleet, "m1", sprint.Done), "thirteen since the reset")

	tidied, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "history"})
	require.NoError(t, err)
	require.Len(t, tidied.Record.Rows, 1)
	var moved []string
	for _, c := range tidied.Record.Rows[0].Moved {
		moved = append(moved, c.ID)
		assert.True(t, c.Finished.After(reset.Mark.At), "%s finished after the reset", c.ID)
	}
	assert.Len(t, moved, 3, "three s1 cards from after the reset")
	assert.NotContains(t, moved, kept, "s2-1's card from before the reset stays: its primary is merging")
	m, err := r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, sprint.ResetRow{OK: 1}, m.Rows["m1"], "no card from before the mark moved: the mark stands")
	assert.Equal(t, "10", cell(r.resetView().fleet, "m1", sprint.Done), "the cards since the mark still on the row, never s2-1's")
}

// A reset under an operation id writes one mark: the same id again returns it and writes
// nothing; the same id with another reason, or an id another verb's step recorded, is refused.
func TestAResetUnderAnOperationIDIsWrittenOnce(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.landOnM1(2)
	first, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now", Op: "reset-1"})
	require.NoError(t, err)
	assert.False(t, first.Replay)
	assert.Equal(t, "reset-1", first.Mark.Op)
	r.mu.Lock()
	r.now = r.now.Add(time.Minute)
	r.mu.Unlock()
	again, err := r.st.ResetStats(r.ctx, store.ResetReq{Reason: "count from now", Op: "reset-1"})
	require.NoError(t, err)
	assert.True(t, again.Replay, "a retry returns the recorded mark")
	assert.Equal(t, first.Mark.At, again.Mark.At, "and writes no second")
	m, err := r.st.StatsReset(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, first.Mark.At, m.At)

	_, err = r.st.ResetStats(r.ctx, store.ResetReq{Reason: "another reason", Op: "reset-1"})
	var conflict *store.OpConflictError
	require.ErrorAs(t, err, &conflict, "the same id with other arguments is not a retry")
	assert.True(t, conflict.OtherArgs)

	r.must(store.Step{Verb: "poke", Load: []string{sprint.Fleet}, CallerOp: "poke-1", Plan: func(*sprint.Snapshot) sprint.Plan { return sprint.Plan{} }})
	if _, err = r.st.ResetStats(r.ctx, store.ResetReq{Reason: "x", Op: "poke-1"}); err == nil {
		// a step that changed nothing may record no result: then the id is free, and the
		// reset is written under it once
		m, _ := r.st.StatsReset(r.ctx)
		assert.Equal(t, "poke-1", m.Op)
	} else {
		require.ErrorAs(t, err, &conflict, "an id another verb recorded is refused")
	}
	_, err = r.st.ResetStats(r.ctx, store.ResetReq{Reason: "x", Op: "bad~op"})
	require.Error(t, err)
}

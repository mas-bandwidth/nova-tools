package sprint_test

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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

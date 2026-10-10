package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// etaWork is a work table of one stream with these cards ready and landed.
func etaWork(ready, landed int64) ntable.Table {
	return ntable.Table{
		Columns: []ntable.Column{{Name: sprint.Ready, Projection: ntable.Count}, {Name: sprint.Landed, Projection: ntable.Count}},
		Rows:    []ntable.Row{{Key: "s1", Cells: []ntable.Cell{{Count: ready}, {Count: landed}}}},
	}
}

// The sprint line's ETA is an estimate while the sprint runs (Glenn, 2026-10-01 9:17 PM
// ET: "an estimate, based on the number of cards remaining, and the average time per-card
// to land"): the cards left at the cards landed an hour, in whole minutes rounded up, never
// seconds ("i don't need seconds. round up to minute"). With fewer than five landed, or no
// rate, the ETA reads a dash; with every card landed there is no ETA.
func TestTheSprintLineEstimatesItsETAFromTheCardsLeftAndTheLandingRate(t *testing.T) {
	t.Parallel()
	line := func(ready, landed int64, in time.Duration) string {
		w := etaWork(ready, landed)
		rate := 0.0
		if in > 0 {
			rate = float64(landed) / in.Hours()
		}
		return summary(w, 0, etaMinutes(w, rate))
	}
	// 250 of 1000 landed in 5 minutes is 3,000 an hour: the 750 left take 15 minutes
	assert.Equal(t, "250/1000 25.0% -> ETA 15m", line(750, 250, 5*time.Minute))
	assert.Equal(t, "1/3 33.3% -> ETA -", line(2, 1, 10*time.Second), "one landed is under the five-card threshold: no estimate")
	assert.Equal(t, "100/1000 10.0% -> ETA 1h12m", line(900, 100, 8*time.Minute))
	assert.Equal(t, "0/3 0.0% -> ETA -", line(3, 0, time.Minute), "nothing landed: no rate to estimate from")
	assert.Equal(t, "10/30 33.3% -> ETA -", line(20, 10, 0), "no rate: no estimate, never a number")
	assert.Equal(t, "3/3 100.0% done", line(0, 3, time.Minute), "every card landed: done, no ETA")
}

// The ETA reads a dash until five cards have landed in the epoch: four landed
// give no rate to estimate from, five do.
func TestTheETAReadsDashUntilFiveCardsHaveLanded(t *testing.T) {
	t.Parallel()
	line := func(ready, landed int64) string {
		w := etaWork(ready, landed)
		return summary(w, 0, etaMinutes(w, float64(landed)*60))
	}
	assert.Equal(t, "4/10 40.0% -> ETA -", line(6, 4))
	assert.Equal(t, "5/10 50.0% -> ETA 1m", line(5, 5))
}

// The ETA is the time until every card on the work table has landed (Glenn, 2026-10-02
// 9:57 PM ET, at a dashboard showing 347 of 2,846 landed, ETA 1h 36m, throughput 2 cards
// an hour: "Please update the ETA on the sprint. It's OBVIOUSLY wrong." and "it's the ETA to
// all cards being done, not the cards that are in flight or not blocked"): every card not
// landed, the 2,443 held behind sentinels too, at the landings of the last hour of running
// time, or the whole sprint's average when fewer than five landed in it; no rate, a dash.
// From a day on it reads in days and hours, the hours rounded up.
func TestTheETAIsToEveryCardLandedHeldCardsIncluded(t *testing.T) {
	t.Parallel()
	// the live sprint: 2,846 cards, 347 landed, 2,443 held, 56 ready or in flight
	w := ntable.Table{
		Columns: []ntable.Column{{Name: sprint.Waiting, Projection: ntable.Count}, {Name: sprint.Ready, Projection: ntable.Count}, {Name: sprint.Landed, Projection: ntable.Count}},
		Rows:    []ntable.Row{{Key: "s1", Cells: []ntable.Cell{{Count: 2443}, {Count: 56}, {Count: 347}}}},
	}
	const held = 2443
	first := time.Date(2026, 10, 2, 12, 57, 0, 0, time.UTC)
	now := first.Add(9 * time.Hour)
	opened := []sprint.Span{{From: first.Add(-time.Hour), To: first}} // every epoch begins STOPPED
	// landings is n stamps evenly over [from, to)
	landings := func(n int, from, to time.Time) []time.Time {
		out := make([]time.Time, n)
		for i := range out {
			out[i] = from.Add(to.Sub(from) * time.Duration(i) / time.Duration(n))
		}
		return out
	}
	lastHour := now.Add(-59 * time.Minute)
	cases := []struct {
		name    string
		landed  []time.Time
		spans   []sprint.Span
		rate    float64
		summary string
	}{
		{"40 in the last hour: 2,499 left at 40 an hour, 62h29m",
			append(landings(307, first, now.Add(-time.Hour)), landings(40, lastHour, now)...), opened,
			40, "347/2846 12.2% held=2443 -> ETA 2d15h"},
		{"2 in the last hour, under five: the whole sprint's 347 over 9 h",
			append(landings(345, first, now.Add(-time.Hour)), landings(2, lastHour, now)...), opened,
			347.0 / 9, "347/2846 12.2% held=2443 -> ETA 2d17h"},
		{"0 in the last hour: the whole sprint's 347 over 9 h, 38.6 an hour",
			landings(347, first, now.Add(-time.Hour)), opened,
			347.0 / 9, "347/2846 12.2% held=2443 -> ETA 2d17h"},
		{"a stop is not running time: the hour reaches back over it",
			append(landings(337, first, now.Add(-3*time.Hour)), landings(10, now.Add(-115*time.Minute), now.Add(-90*time.Minute))...),
			append(opened, sprint.Span{From: now.Add(-90 * time.Minute), To: now.Add(-30 * time.Minute)}),
			10, "347/2846 12.2% held=2443 -> ETA 10d10h"},
		{"no stamp and no start: no rate, a dash", nil, nil, 0, "347/2846 12.2% held=2443 -> ETA -"},
		{"stamps but no start: no rate, a dash", landings(347, first, now), nil, 0, "347/2846 12.2% held=2443 -> ETA -"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			start := first
			if c.spans == nil {
				start = time.Time{}
			}
			rate := sprint.LandingRate(c.landed, 347, c.spans, start, now)
			assert.InDelta(t, c.rate, rate, 1e-9)
			assert.Equal(t, c.summary, summary(w, held, etaMinutes(w, rate)))
		})
	}
	// at the 2 an hour the dashboard's tile showed, every card is 1,249.5 hours away
	assert.Equal(t, int64(74970), etaMinutes(w, 2))
	assert.Equal(t, "347/2846 12.2% held=2443 -> ETA 52d2h", summary(w, held, etaMinutes(w, 2)))
}

// where shows the held cards of the table on its header line and in --json, and the
// tables below it are as they were: a held sentinel and the wave behind it are held, a
// card of another stream is not.
func TestWhereShowsHeldCardsApart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream w --sentinel gate --held --actor lead")
	ta.ok("add --stream w a b --actor lead")
	ta.ok("add --stream v c --one --actor lead")
	ta.ok("add --stream v d --one --needs a --actor lead")
	ta.ok("start --actor lead") // a STOPPED machine's header is STOPPED alone
	assert.Contains(t, ta.ok("where"), "0/5 0.0% held=4 -> ETA -", "where's header")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(4), w.Held, "where --json: %+v", w)
	assert.Equal(t, "3", w.Tables[sprint.Work]["w"][sprint.Waiting], "the work table is as it was: %+v", w.Tables)
}

// The view shows the largest estimate of the last 10 s, so the value is stable (Glenn,
// 2026-10-01: "take largest ETA in last 10 secs, so it is a stable value"): a smaller
// estimate is shown only once every larger one is 10 s old. The hold is over the same
// cards to land (Glenn, 2026-10-02: "When you add new cards, the ETA needs to be made
// dirty and recalculated."): an estimate over another count is dirty and forgotten.
func TestTheViewHoldsTheLargestETAOfTheLastTenSeconds(t *testing.T) {
	t.Parallel()
	a := &app{}
	t0 := time.Date(2026, 10, 1, 21, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	k := etaKey{all: 100}
	assert.Equal(t, int64(40), a.heldETA(at(0), k, 40))
	assert.Equal(t, int64(40), a.heldETA(at(2), k, 35), "a smaller estimate inside 10 s: the larger is held")
	assert.Equal(t, int64(42), a.heldETA(at(4), k, 42), "a larger estimate shows at once")
	assert.Equal(t, int64(42), a.heldETA(at(13), k, 30), "40 has aged out, 42 has not")
	assert.Equal(t, int64(30), a.heldETA(at(14), k, 30), "42 is 10 s old: the largest of the last 10 s is 30")
	assert.Equal(t, int64(0), a.heldETA(at(15), k, 0), "no estimate is shown as none")
	assert.Equal(t, int64(5), a.heldETA(at(16), k, 5), "and nothing held survives it")
	assert.Equal(t, int64(9), a.heldETA(at(17), k, 9))
	assert.Equal(t, int64(4), a.heldETA(at(18), etaKey{all: 90}, 4), "cards dropped: the held estimate is dirty, the new one shows at once")
	assert.Equal(t, int64(4), a.heldETA(at(19), etaKey{all: 90}, 3), "and is held over the new count")
	assert.Equal(t, int64(2), a.heldETA(at(20), etaKey{all: 90, held: 5}, 2), "cards held: dirty")
	assert.Equal(t, int64(3), a.heldETA(at(21), etaKey{all: 90}, 3), "cards released: dirty")
}

// An add, a drop and a release on a running sprint make its ETA dirty (Glenn, 2026-10-02:
// "When you add new cards, the ETA needs to be made dirty and recalculated.";
// nova-tools#5171): the tick that drains the change puts it on the work table, and the next
// read of where and where --json shows the estimate recomputed over the new count of cards to
// land, held ones too, at the rate measured (the landings over the running time, all in the last hour here), with no
// 10 s hold of the estimate made over the count before. No real time: the clock is the test's.
func TestTheETAIsRecomputedOnTheTickAfterAnAddADropOrARelease(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 30")
	ta.ok("start")
	var w whereView
	for round := 1; ; round++ {
		require.Less(t, round, 200, "five never landed: %s", ta.ok("where"))
		ta.ok("tick")
		if ta.json("where", &w); w.Landed >= 5 {
			break
		}
		ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --every 1s --fail 0 --broken 0 --stuck 0 --cross 0 --batch 10 --take 20 --reads 20", round))
		ta.coordinate()
	}
	ta.mu.Lock()
	ta.now = ta.now.Add(40 * time.Minute) // a rate in minutes a card, not seconds
	ta.mu.Unlock()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	// read is where --json's summary, and the ETA the measured rate gives its count
	read := func() (whereView, string) {
		t.Helper()
		var v whereView
		ta.json("where", &v)
		since, started := st.SinceFirstStart(context.Background())
		require.True(t, started)
		require.Less(t, since, time.Hour, "every landing is in the last hour of running time")
		left := v.All - v.Landed // held cards too
		want := int64(math.Ceil(float64(left) * 60 / (float64(v.Landed) / since.Hours())))
		require.Less(t, want, int64(24*60))
		if want < 60 {
			return v, fmt.Sprintf("-> ETA %dm", want)
		}
		return v, fmt.Sprintf("-> ETA %dh%dm", want/60, want%60)
	}
	before, want := read()
	require.Contains(t, before.Summary, want, "the estimate before the add")
	stamps, err := st.LandedAt(context.Background())
	require.NoError(t, err)
	require.Len(t, stamps, int(before.Landed), "every landed card carries its landed stamp")
	landed := before.Landed

	ta.ok("add --stream s2 --count 20")
	ta.ok("tick")
	added, want := read()
	require.Equal(t, landed, added.Landed, "nothing landed between: the rate is the same")
	require.Equal(t, before.All+20, added.All, "the tick drained the add")
	require.Contains(t, added.Summary, want, "20 more cards at the rate measured")
	require.NotEqual(t, before.Summary, added.Summary)

	ta.ok("drop s2-1 s2-2 s2-3 s2-4 s2-5 s2-6 s2-7 s2-8 s2-9 s2-10 s2-11 s2-12 --reason 'not wanted'")
	ta.ok("tick")
	dropped, want := read()
	require.Equal(t, landed, dropped.Landed)
	require.Equal(t, added.All-12, dropped.All, "the tick drained the drop")
	require.Contains(t, dropped.Summary, want, "12 fewer cards at once, inside 10 s of the larger estimate")

	ta.ok("add --stream s3 h1 h2 h3 h4 h5 h6 --held")
	ta.ok("tick")
	heldBack, want := read()
	require.Equal(t, int64(6), heldBack.Held)
	require.Contains(t, heldBack.Summary, "held=6 "+want, "held cards are in the estimate")
	ta.ok("release h1 h2 h3 h4 h5 h6 --reason 'the wave is loaded'")
	ta.ok("tick")
	released, want := read()
	require.Equal(t, landed, released.Landed)
	require.Zero(t, released.Held)
	require.Contains(t, released.Summary, want, "6 released cards are in the estimate")
	require.NotEqual(t, heldBack.Summary, released.Summary)
}

// A read of the landed stamps that fails (the landed column busy with landings) leaves
// the whole sprint's average: where and where --json still answer, with an estimate,
// never a failed view. where reads the stamps only when it cannot take the tick's
// where record, so the table is moved under a STOPPED machine that has not ticked since:
// where reads the cards, and the read of the stamps fails. Nothing waits here, so the
// landed column is the only work table records where reads.
func TestWhereKeepsTheWholeSprintAverageWhenTheLandedStampsFailToRead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 30")
	ta.ok("start")
	var w whereView
	for round := 1; ; round++ {
		require.Less(t, round, 200, "five never landed: %s", ta.ok("where"))
		ta.ok("tick")
		if ta.json("where", &w); w.Landed >= 5 {
			break
		}
		ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --every 1s --fail 0 --broken 0 --stuck 0 --cross 0 --batch 10 --take 20 --reads 20", round))
		ta.coordinate()
	}
	require.Zero(t, w.Held)
	ta.mu.Lock()
	ta.now = ta.now.Add(2 * time.Hour) // every landing is over an hour old: the average either way
	ta.mu.Unlock()
	ta.ok("stop --reason r --until 9999h")
	ta.ok("add --stream s2 --count 1 --one") // the table moves; no tick counts it
	ta.json("where", &w)
	require.Zero(t, w.Held, "nothing waits: %s", w.Summary)
	healthy := w.Summary
	require.Contains(t, healthy, "-> ETA ", "an estimate: %s", healthy)
	require.NotContains(t, healthy, "-> ETA -")

	busy := errors.New("the tables are busy")
	failed := 0
	ta.m.Fail = func(point string) error {
		if point == "readset "+sprint.Work {
			failed++
			return busy
		}
		return nil
	}
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	_, err := st.LandedAt(context.Background())
	require.ErrorIs(t, err, busy, "the stamps do not read")
	failed = 0
	var degraded whereView
	ta.json("where", &degraded)
	require.Positive(t, failed, "where read the stamps, and the read failed")
	assert.Equal(t, healthy, degraded.Summary, "the same estimate, from the whole sprint's average")
	ta.ok("where") // the frame answers too (its header is STOPPED)
}

package main

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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
// to land"): the cards left, each at the time from the first start over the cards landed,
// in whole minutes rounded up, never seconds ("i don't need seconds. round up to minute").
// With fewer than five landed, or no start known, there is no rate and the ETA reads a
// dash; with every card landed there is no ETA.
func TestTheSprintLineEstimatesItsETAFromTheCardsLeftAndTheAverageTimeToLand(t *testing.T) {
	t.Parallel()
	line := func(ready, landed int64, since time.Duration, started bool) string {
		w := etaWork(ready, landed)
		return summary(w, 0, etaMinutes(w, 0, since, started))
	}
	// 250 of 1000 landed in 5 minutes is 1.2 s a card: the 750 left take 15 minutes
	assert.Equal(t, "250/1000 25.0% -> ETA 15m", line(750, 250, 5*time.Minute, true))
	assert.Equal(t, "1/3 33.3% -> ETA -", line(2, 1, 10*time.Second, true), "one landed is under the five-card threshold: no estimate")
	assert.Equal(t, "1/3 33.3% -> ETA -", line(2, 1, 31*time.Second, true), "one landed is under the five-card threshold: no estimate")
	assert.Equal(t, "100/1000 10.0% -> ETA 1h12m", line(900, 100, 8*time.Minute, true))
	assert.Equal(t, "0/3 0.0% -> ETA -", line(3, 0, time.Minute, true), "nothing landed: no rate to estimate from")
	assert.Equal(t, "1/3 33.3% -> ETA -", line(2, 1, 0, false), "no first start known: no estimate")
	assert.Equal(t, "3/3 100.0% done", line(0, 3, time.Minute, true), "every card landed: done, no ETA")
}

// The ETA reads a dash until five cards have landed in the epoch: four landed
// give no rate to estimate from, five do.
func TestTheETAReadsDashUntilFiveCardsHaveLanded(t *testing.T) {
	t.Parallel()
	line := func(ready, landed int64) string {
		w := etaWork(ready, landed)
		return summary(w, 0, etaMinutes(w, 0, time.Minute, true))
	}
	assert.Equal(t, "4/10 40.0% -> ETA -", line(6, 4))
	assert.Equal(t, "5/10 50.0% -> ETA 1m", line(5, 5))
}

// The ETA is over the dealable cards (nova-tools#5096 item 16, the wave-2 card builder:
// "where's ETA counts held cards as dealable (2h38m to 3h30m on adding held work)"; the
// coordinator, quoting Glenn: "I'd like to really really load up the sprint in waiting,
// and stick sentinels in"): the cards behind a sentinel not released, or admitted held,
// show apart as held=N, and the cards left that the estimate counts are the rest.
func TestTheETAIsOverDealableCardsAndHeldCardsShowApart(t *testing.T) {
	t.Parallel()
	line := func(ready, landed, held int64, since time.Duration) string {
		w := etaWork(ready, landed)
		return summary(w, held, etaMinutes(w, held, since, true))
	}
	// 250 landed in 5 minutes is 1.2 s a card: of the 750 left, 500 are held, and the
	// 250 dealable take 5 minutes
	assert.Equal(t, "250/1000 25.0% held=500 -> ETA 5m", line(750, 250, 500, 5*time.Minute))
	assert.Equal(t, "1/3 33.3% held=2 -> ETA -", line(2, 1, 2, time.Minute), "every card left is held: nothing to estimate")
	assert.Equal(t, "250/1000 25.0% -> ETA 15m", line(750, 250, 0, 5*time.Minute), "none held: the line as before")
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
	ta.ok("add --stream v c --actor lead")
	ta.ok("add --stream v d --needs a --actor lead")
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
// land at the rate measured, the time from the first start over the cards landed, with no
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
		left := v.All - v.Landed - v.Held
		want := int64(math.Ceil(float64(since) * float64(left) / float64(v.Landed) / float64(time.Minute)))
		if want < 60 {
			return v, fmt.Sprintf("-> ETA %dm", want)
		}
		return v, fmt.Sprintf("-> ETA %dh%dm", want/60, want%60)
	}
	before, want := read()
	require.Contains(t, before.Summary, want, "the estimate before the add")
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
	require.Contains(t, heldBack.Summary, "held=6 "+want, "held cards are not in the estimate")
	ta.ok("release h1 h2 h3 h4 h5 h6 --reason 'the wave is loaded'")
	ta.ok("tick")
	released, want := read()
	require.Equal(t, landed, released.Landed)
	require.Zero(t, released.Held)
	require.Contains(t, released.Summary, want, "6 released cards are in the estimate")
	require.NotEqual(t, heldBack.Summary, released.Summary)
}

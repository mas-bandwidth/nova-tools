package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
// With none landed, or no start known, there is no rate and the word stands alone; with
// every card landed there is no ETA.
func TestTheSprintLineEstimatesItsETAFromTheCardsLeftAndTheAverageTimeToLand(t *testing.T) {
	t.Parallel()
	line := func(ready, landed int64, since time.Duration, started bool) string {
		w := etaWork(ready, landed)
		return summary(w, 0, etaMinutes(w, 0, since, started))
	}
	// 250 of 1000 landed in 5 minutes is 1.2 s a card: the 750 left take 15 minutes
	assert.Equal(t, "250/1000 25.0% -> ETA 15m", line(750, 250, 5*time.Minute, true))
	assert.Equal(t, "1/3 33.3% -> ETA 1m", line(2, 1, 10*time.Second, true), "20 s left is 1m")
	assert.Equal(t, "1/3 33.3% -> ETA 2m", line(2, 1, 31*time.Second, true), "62 s left is 2m")
	assert.Equal(t, "100/1000 10.0% -> ETA 1h12m", line(900, 100, 8*time.Minute, true))
	assert.Equal(t, "0/3 0.0% -> ETA", line(3, 0, time.Minute, true), "nothing landed: no rate to estimate from")
	assert.Equal(t, "1/3 33.3% -> ETA", line(2, 1, 0, false), "no first start known: no estimate")
	assert.Equal(t, "3/3 100.0% done", line(0, 3, time.Minute, true), "every card landed: done, no ETA")
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
	assert.Equal(t, "1/3 33.3% held=2 -> ETA", line(2, 1, 2, time.Minute), "every card left is held: nothing to estimate")
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
	assert.Contains(t, ta.ok("where"), "0/5 0.0% held=4 -> ETA", "where's header")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(4), w.Held, "where --json: %+v", w)
	assert.Equal(t, "3", w.Tables[sprint.Work]["w"][sprint.Waiting], "the work table is as it was: %+v", w.Tables)
}

// The view shows the largest estimate of the last 10 s, so the value is stable (Glenn,
// 2026-10-01: "take largest ETA in last 10 secs, so it is a stable value"): a smaller
// estimate is shown only once every larger one is 10 s old.
func TestTheViewHoldsTheLargestETAOfTheLastTenSeconds(t *testing.T) {
	t.Parallel()
	a := &app{}
	t0 := time.Date(2026, 10, 1, 21, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	assert.Equal(t, int64(40), a.heldETA(at(0), 40))
	assert.Equal(t, int64(40), a.heldETA(at(2), 35), "a smaller estimate inside 10 s: the larger is held")
	assert.Equal(t, int64(42), a.heldETA(at(4), 42), "a larger estimate shows at once")
	assert.Equal(t, int64(42), a.heldETA(at(13), 30), "40 has aged out, 42 has not")
	assert.Equal(t, int64(30), a.heldETA(at(14), 30), "42 is 10 s old: the largest of the last 10 s is 30")
	assert.Equal(t, int64(0), a.heldETA(at(15), 0), "no estimate is shown as none")
	assert.Equal(t, int64(5), a.heldETA(at(16), 5), "and nothing held survives it")
}

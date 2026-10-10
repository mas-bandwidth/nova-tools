package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// whereTripsMax is the most store round trips one where --json makes at 3,000
// cards once the tick has counted the where record: none of them a card's
// record but the streams' control cards (docs/SPEC-SPRINT.md section 1): the
// shapes twice (the view's tables, the stream clocks'), the fence, the coordinator,
// four records (the seat, the machine's with the where record, the friends, the
// goals) and the streams' control cards. Measured on this fixture: 22 on the code
// before the record (5183's head, 7 of them reads of card records), 19 on this
// code's own read of the cards when it cannot take the record, 9 with it.
const whereTripsMax = 9

// whereLogLinesMax is the most log lines one where --json reads once the tick has counted
// the where record: none. Its landedSeries buckets the record's landings
// (store.WhereRecord.Series); before 2026-10-10 it read the epoch's log whole every call,
// 695,000 lines on the live store, which no round-trip count shows (an in-memory page is
// one exchange).
const whereLogLinesMax = 0

// bigSprint is the live sprint's shape at 3,000 cards (2026-10-02 10:13 PM ET,
// 2,843 cards, where 5.2 to 7.0 s on the live store): five streams of a held sentinel
// and 499 cards behind it (2,500 held), 350 landed over the last two hours of
// running time, and 150 ready to deal. The machine is RUNNING and has not
// ticked: no where record yet, as on the live store before this change.
func bigSprint(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --coordinator lead")
	for i := 1; i <= 5; i++ {
		ta.ok(fmt.Sprintf("add --stream w%d --sentinel gate%d --held --actor lead", i, i))
		ta.ok(fmt.Sprintf("add --stream w%d --count 499 --actor lead", i))
	}
	// done keeps one card not landed, so no tick archives it (stream archive) and its 350
	// landed cards stay in the headline
	ta.ok("add --stream done --count 351 --actor lead")
	ta.ok("add --stream f --count 149 --actor lead")
	ta.ok("start --actor lead")
	ta.mu.Lock()
	ta.now = ta.now.Add(2 * time.Hour)
	ta.mu.Unlock()
	// the 350 landed one every 20 s over the two hours: a store write, as the
	// merges of those hours made them
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	ctx := context.Background()
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	require.NoError(t, err)
	now := ta.a.now()
	var ms []ntable.BatchMemberEntry
	for i, c := range snap.Work.Cell("done", sprint.Ready)[:350] {
		at := now.Add(-2 * time.Hour).Add(time.Duration(i+1) * 20 * time.Second)
		ms = append(ms, ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)},
			Move: &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Landed}, Set: map[string]string{"landed": at.UTC().Format(time.RFC3339)}})
	}
	rev := snap.Work.Revision
	for k := 0; k < len(ms); k += 100 {
		rc, err := ta.m.Apply(ctx, ntable.BatchManifest{Schema: 1, Table: st.Names.Table(sprint.Work), Epoch: "0",
			ExpectedTableRevision: fmt.Sprint(rev), OperationID: fmt.Sprintf("seed-landed-%d", k), Members: ms[k:min(k+100, len(ms))]})
		require.NoError(t, err)
		rev = rc.After
	}
	return ta
}

// whereJSON is one where --json and the store's exchanges it made, by kind
// (the test's beats before it not counted).
func (ta *testApp) whereJSON() (whereView, map[string]int) {
	ta.t.Helper()
	ta.beat()
	ta.m.Calls = map[string]int{}
	var out, errb bytes.Buffer
	require.Zero(ta.t, ta.a.run([]string{"where", "--json"}, &out, &errb), "where --json: %s", errb.String())
	calls := ta.m.Calls
	ta.m.Calls = map[string]int{}
	var v whereView
	require.NoError(ta.t, json.Unmarshal(out.Bytes(), &v))
	return v, calls
}

// trips is the exchanges of every kind.
func trips(calls map[string]int) int {
	n := 0
	for _, c := range calls {
		n += c
	}
	return n
}

// where reads table cells and one record, not every card (the owner's rules: one
// read per tick, and a view never recomputed from every row when the table
// carries the answer; the requirement, where under 1 s at 3,000 cards, at 2,843
// it took 5.2 to 7.0 s on the live store, reading the 2,443 waiting and 347 landed
// cards' records): at 3,000 cards, once the tick has counted the where record,
// where --json reads no card's record but the streams' control cards, in at most
// whereTripsMax round trips, and says what reading every card says. Before the
// first tick (the live store, which has no record) it reads the cards, and
// answers the same. No real time: the clock is the test's; the wall-clock bound
// is behind -tags perf (where_cost_perf_test.go).
func TestWhereReadsTheTableNotEveryCardAtThreeThousandCards(t *testing.T) {
	t.Parallel()
	ta := bigSprint(t)
	byCards, cardCalls := ta.whereJSON()
	require.Equal(t, int64(3000), byCards.All)
	require.Equal(t, int64(350), byCards.Landed)
	require.Equal(t, int64(2500), byCards.Held)
	require.Greater(t, cardCalls["readset"], 1, "no record yet: the waiting and landed cards are read: %v", cardCalls)
	require.Contains(t, byCards.Summary, "held=2500 -> ETA ", "an estimate at the hour's rate")
	require.NotContains(t, byCards.Summary, "ETA -")

	ta.ok("tick")
	lines := ta.m.LogLines
	counted, calls := ta.whereJSON()
	logRead := ta.m.LogLines - lines
	n := trips(calls)
	t.Logf("where --json at 3,000 cards: %d round trips %v, %d log lines; before the record %d %v", n, calls, logRead, trips(cardCalls), cardCalls)
	require.Equal(t, 1, calls["readset"], "one read of records, the streams' control cards (stream clocks): %v", calls)
	require.Zero(t, calls["cells"], "no cell's member ids: %v", calls)
	require.LessOrEqual(t, n, whereTripsMax, "where --json made %d round trips, at most %d: %v", n, whereTripsMax, calls)
	// the landed series is the record's landings, never the log read whole: on the live
	// store (2026-10-10) that read was 695,000 lines and 82% of where's 12.7 s
	require.LessOrEqual(t, logRead, whereLogLinesMax, "where --json read %d log lines for its landed series, at most %d", logRead, whereLogLinesMax)
	require.NotNil(t, counted.LandedSeries, "where --json carries landedSeries from the record")
	require.Equal(t, 350, counted.LandedSeries.Totals.Fleet+counted.LandedSeries.Totals.Friends+counted.LandedSeries.Totals.Unknown,
		"the 350 landed in the last two hours are in the series")
	require.Equal(t, byCards.All, counted.All)
	require.Equal(t, byCards.Landed, counted.Landed)
	require.Equal(t, byCards.Held, counted.Held, "the record's held cards are the cards'")
	require.Equal(t, byCards.Summary, counted.Summary, "the record's ETA is the cards'")
}

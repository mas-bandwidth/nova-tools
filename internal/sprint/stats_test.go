package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// statsSnapshot is a pass with known numbers, the clock in seconds from t0:
//
//   - p1, landed: admitted 0, accepted 60, landed 70, two attempts. w1 on m1 first
//     dealt 4, taken 5, failed at 15 (wall 6); w2 on m1 dealt 20, taken 23, ok at 40
//     (wall 10). At attempt 2 reader-a was asked at 40, began 42 and read 52 (wall 8);
//     reader-b's read was asked at 40 and retired unplaced, never begun.
//   - p2, in review: admitted 0, one attempt, w1 on m2 dealt 1 (first dealt 2), taken
//     2, ok at 12 (wall 8).
//   - a sentinel, which is no primary of the pass.
//
// The cost records on the primaries are the routes' takes: on flash-a w1's failure
// (wall 6) and w2's ok (wall 10); on flash-b a take the provider failed (wall 2) and
// one with no result (wall 4); a launch refused at staging; reader-a's read on pro-a
// (wall 8); reader-b's run returned with no route on its record (its card's is pro-b);
// p2's take on a pinned model (wall 8).
func statsSnapshot() *Snapshot {
	t0 := time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC)
	at := func(sec int) string { return stamp(t0.Add(time.Duration(sec) * time.Second)) }
	s := &Snapshot{Epoch: 7, Work: NewTable(Work), Fleet: NewTable(Fleet), Readers: NewTable(Readers)}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{"m1", "m2"})
	s.Readers.SetRows([]string{"reader-a", "reader-b"})
	record := func(p *Card, c Consumer, wall string) {
		c.Key = c.Card + "#" + c.End
		if wall != "" {
			c.Usage = cardcost.ParseUsage("wall=" + wall)
		} else {
			c.Usage = cardcost.NoUsage()
		}
		p.Fields[FieldCostRecord+c.Key] = c.line()
	}

	p1 := &Card{ID: "p1", Row: "s1", Col: Landed, Score: 1, Fields: map[string]string{
		"attempt": "2", "admitted": at(0), "accepted": at(60), "landed": at(70)}}
	record(p1, Consumer{Kind: "work", Card: "p1.w1", Route: "flash-a", End: "failed"}, "6.00s")
	record(p1, Consumer{Kind: "work", Card: "p1.w1", Take: 1, Route: "flash-b", End: "provider failure: 529"}, "2.00s")
	record(p1, Consumer{Kind: "work", Card: "p1.w1", Route: "flash-a", End: "staging refused: no mirror"}, "")
	record(p1, Consumer{Kind: "work", Card: "p1.w2", Route: "flash-a", End: "ok"}, "10.00s")
	record(p1, Consumer{Kind: "read", Card: "p1.r2.reader-a", Route: "pro-a", End: "ok"}, "8.00s")
	record(p1, Consumer{Kind: "read", Card: "p1.r2.reader-b", Take: 1, End: "returned"}, "")
	p2 := &Card{ID: "p2", Row: "s1", Col: Review, Score: 2, Fields: map[string]string{"attempt": "1", "admitted": at(0)}}
	record(p2, Consumer{Kind: "work", Card: "p2.w1", Take: 1, Route: "flash-b", End: "no result: child wrote nothing"}, "4.00s")
	record(p2, Consumer{Kind: "work", Card: "p2.w1", Route: RoutePin, Model: "opencode/x", End: "ok"}, "8.00s")
	sentinel := &Card{ID: "stop", Row: "s1", Col: Waiting, Score: 3, Fields: map[string]string{"kind": "sentinel", "admitted": at(0), "attempt": "1"}}
	for _, c := range []*Card{p1, p2, sentinel} {
		s.Work.Put(c)
	}

	s.Fleet.Put(&Card{ID: "p1.w1", Row: "m1", Col: DoneFailed, Fields: map[string]string{"member": "m1", "attempt": "1", "ok": "no",
		"first_dealt": at(4), "dealt": at(4), "taken": at(5), "finished": at(15), FieldUsage: "wall=6.00s"}})
	s.Fleet.Put(&Card{ID: "p1.w2", Row: "m1", Col: DoneOK, Fields: map[string]string{"member": "m1", "attempt": "2", "ok": "yes",
		"first_dealt": at(20), "dealt": at(20), "taken": at(23), "finished": at(40), FieldUsage: "wall=10.00s budget=1/2"}})
	s.Fleet.Put(&Card{ID: "p2.w1", Row: "m2", Col: DoneOK, Fields: map[string]string{"member": "m2", "attempt": "1", "ok": "yes",
		"first_dealt": at(2), "dealt": at(1), "taken": at(2), "finished": at(12), FieldUsage: "wall=8.00s"}})
	s.Readers.Put(&Card{ID: "p1.r2.reader-a", Row: "reader-a", Col: "ok", Fields: map[string]string{"reader": "reader-a",
		"asked": at(40), "begun": at(42), "read": at(52), FieldUsage: "wall=8.00s", FieldRoute: "pro-a"}})
	s.Readers.Put(&Card{ID: "p1.r2.reader-b", Fields: map[string]string{"reader": "reader-b", "asked": at(40), "retired": at(41), FieldRoute: "pro-b"}})
	return s
}

func TestStatsRecordsNameEveryAttemptsWorkAndReadCards(t *testing.T) {
	t.Parallel()
	rec := StatsRecords(statsSnapshot())
	assert.ElementsMatch(t, []string{"p1.w1", "p1.w2", "p2.w1"}, rec[Fleet])
	assert.ElementsMatch(t, []string{"p1.r1.reader-a", "p1.r1.reader-b", "p1.r2.reader-a", "p1.r2.reader-b", "p2.r1.reader-a", "p2.r1.reader-b"}, rec[Readers])
}

func TestStatsArePassNumbersFromTheCards(t *testing.T) {
	t.Parallel()
	ps := Stats(statsSnapshot())
	m := func(med, max float64, n int) Measure { return Measure{Median: med, Max: max, N: n} }
	assert.Equal(t, uint64(7), ps.Epoch)
	assert.Equal(t, 2, ps.Primaries, "a sentinel is no primary of the pass")
	assert.Equal(t, Stages{
		DealWait:      m(3, 4, 2), // first dealt: 4 and 2 after admitted; an even count's median is the mean of the middle two
		FinishToReads: m(20, 20, 1),
		AcceptToLand:  m(10, 10, 1),
		Total:         m(70, 70, 1), // p2 has not landed
	}, ps.Stages)
	assert.Equal(t, []MemberStat{
		{Member: "m1", Cards: 2, Failed: 1, TakeWait: m(2, 3, 2), RunWall: m(8, 10, 2), ReportLag: m(5.5, 7, 2)},
		{Member: "m2", Cards: 1, TakeWait: m(1, 1, 1), RunWall: m(8, 8, 1), ReportLag: m(2, 2, 1)},
	}, ps.Work)
	assert.Equal(t, []ReaderStat{
		{Reader: "reader-a", Cards: 1, BeginWait: m(2, 2, 1), RunWall: m(8, 8, 1), ReportLag: m(2, 2, 1)},
		{Reader: "reader-b", Cards: 1}, // retired unbegun: asked of it, no time to measure
	}, ps.Reads)
	assert.Equal(t, []RouteTakes{
		{Route: "flash-a", Takes: 2, OK: 1, Failed: 1, RunWall: m(8, 10, 2)}, // the staging refusal is no take
		{Route: "flash-b", Takes: 2, Provider: 2, RunWall: m(3, 4, 2)},
		{Route: "pin:opencode/x", Takes: 1, OK: 1, RunWall: m(8, 8, 1)},
		{Route: "pro-a", Takes: 1, OK: 1, RunWall: m(8, 8, 1)},
		{Route: "pro-b", Takes: 1, Failed: 1}, // returned unpriced: its card's route
	}, ps.Routes)
}

func TestStatsOfNothingAreEmptyNeverNil(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work), Fleet: NewTable(Fleet), Readers: NewTable(Readers)}
	ps := Stats(s)
	require.NotNil(t, ps.Work)
	require.NotNil(t, ps.Reads)
	require.NotNil(t, ps.Routes)
	assert.Equal(t, Stages{}, ps.Stages)
	assert.Empty(t, StatsRecords(s)[Fleet])
}

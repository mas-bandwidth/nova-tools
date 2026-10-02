package sprint

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The deal goes round the fleet and the ask goes round the readers (errata 3,
// amendment 5; round.go): a rolling index modulo the number of members
// (readers), moved on with every card dealt (read asked), in place of the
// shortest queue, whose ties by name give every card of an idle fleet to the
// first members.

// propsAnswer is what a twin answers of the table properties a query names: the
// ones the table holds.
func propsAnswer(t *Table, names []string) map[string]string {
	var out map[string]string
	for _, n := range names {
		if v, ok := t.Prop(n); ok {
			if out == nil {
				out = map[string]string{}
			}
			out[n] = v
		}
	}
	return out
}

// fleetWorld is a world of the members up at the width (0: the default) and
// n ready primaries in stream s1.
func fleetWorld(t *testing.T, primaries, width int, members ...string) *world {
	t.Helper()
	w := newWorld(t, "reader-a")
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: width}))
	}
	if primaries > 0 {
		w.must(Add(w.s, AddReq{Stream: "s1", Count: primaries}))
	}
	return w
}

// part runs one part of the tick (steps_tick.go) on the world's state and
// applies its plan.
func (w *world) part(fn func(*Snapshot, TickReq) (Plan, int), r TickReq) Plan {
	w.t.Helper()
	p, _ := fn(w.s, r)
	return w.must(p)
}

// place moves a card to row and column in the table, as a write of the store
// would.
func (w *world) place(tb *Table, id, row, col string) {
	w.t.Helper()
	c := tb.Card(id)
	require.NotNil(w.t, c, "no card %s in %s", id, tb.Name)
	c.Row, c.Col = row, col
	c.Rev++
	tb.cells, tb.byPrimary = nil, nil
}

// putWorkCard puts primary p's first work card on the member in the column,
// and the primary in the state that card's place gives it.
func putWorkCard(w *world, p, member, col string, score float64, extra map[string]string) {
	fields := map[string]string{"kind": "work", "primary": p, "stream": "s1", "attempt": "1", "gen": "2", "member": member,
		"dealt": stamp(t0), "untaken_since": stamp(t0), "redeals": "0"}
	for k, v := range extra {
		fields[k] = v
	}
	w.s.Fleet.Put(&Card{ID: p + ".w1", Row: member, Col: col, Score: score, Rev: 1, Fields: fields})
	prim := map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"}
	pcol := Working
	if col == Withdrawn {
		pcol = Ready
	} else {
		prim["work"] = p + ".w1"
	}
	w.s.Work.Put(&Card{ID: p, Row: "s1", Col: pcol, Score: score, Rev: 1, Fields: prim})
}

// putRead puts a read card of the primary at the attempt by the reader, placed
// in the column ("" keeps it unplaced), and names it on the primary (rcards),
// as the ask and the read leave it.
func putRead(w *world, primary string, attempt int, reader, col string) *Card {
	pr := w.s.Work.Card(primary)
	c := &Card{ID: ReadCardID(primary, attempt, reader), Score: pr.Score, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": primary, "stream": pr.Row, "reader": reader, "attempt": itoa(attempt), "head": pr.F("head")}}
	if col != "" {
		c.Row, c.Col = reader, col
	}
	w.s.Readers.Put(c)
	pr.Fields["rcards"] = strings.Join(append(Split(pr.F("rcards")), c.ID), ",")
	return c
}

// eightIdle is a world of 8 up members of room 2 and 30 ready primaries.
func eightIdle(t *testing.T, readers ...string) *world {
	t.Helper()
	w := newWorld(t, readers...)
	for i := 1; i <= 8; i++ {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: fmt.Sprintf("m%d", i)}))
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 30}))
	return w
}

// evenly fails unless every name's count is within one of the others, and,
// when round is set, all are equal.
func evenly(t *testing.T, what string, counts map[string]int, names []string, round bool) {
	t.Helper()
	lo, hi := -1, 0
	for _, n := range names {
		if lo < 0 || counts[n] < lo {
			lo = counts[n]
		}
		hi = max(hi, counts[n])
	}
	require.LessOrEqual(t, hi-lo, 1, "%s: %v, want every count within one of the others (equal after a full round)", what, counts)
	if round {
		require.Equal(t, lo, hi, "%s: %v, want every count within one of the others (equal after a full round)", what, counts)
	}
}

// workIt takes and finishes a work card on its member, ok: the member is idle
// again.
func workIt(w *world, wc *Card) {
	w.t.Helper()
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
}

// 8 idle members of room 2, 30 cards dealt one deal at a time (T3's deal): the
// deals go m1, m2, ..., m8, m1, ...; after each deal every member's count is
// within one of the others, and after each full round all are equal. The
// index is in the store: the fleet table's deal_index.
func TestTheDealGoesRoundTheFleet(t *testing.T) {
	t.Parallel()
	w := eightIdle(t, "reader-a")
	members := w.s.Fleet.Rows()
	dealt := map[string]int{}
	var order []string
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		dealt[wc.Row]++
		order = append(order, wc.Row)
		workIt(w, wc)
		evenly(t, fmt.Sprintf("after deal %d", i), dealt, members, i%8 == 0)
	}
	for i, m := range order {
		want := fmt.Sprintf("m%d", i%8+1)
		require.Equal(t, want, m, "deal %d went to %s, want %s: the deals %v", i+1, m, want, order)
	}
	last, ok := w.s.Fleet.Prop(PropDealIndex)
	require.True(t, ok, "the fleet table's deal_index is %q (%v), want m6", last, ok)
	require.Equal(t, "m6", indexPast(w.s.Fleet.Rows(), last), "the fleet table's deal_index is %q (%v), want m6", last, ok)
	// every member's done is within one of the others
	done := map[string]int{}
	for _, m := range members {
		done[m] = w.s.Fleet.Count(m, DoneOK)
	}
	evenly(t, "done", done, members, false)
}

// The same through the tick's deal (T3): one card ready at a time, dealt by
// the tick, worked at once.
func TestTheTickDealGoesRoundTheFleet(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 0, 0, "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8")
	members := w.s.Fleet.Rows()
	dealt := map[string]int{}
	for i := 1; i <= 24; i++ {
		w.must(Add(w.s, AddReq{Stream: "s1", Count: 1}))
		w.part(TickDeal, TickReq{})
		cs := w.s.Fleet.Column(Ready)
		require.Len(t, cs, 1, "deal %d: nothing dealt", i)
		wc := cs[0]
		want := fmt.Sprintf("m%d", (i-1)%8+1)
		require.Equal(t, want, wc.Row, "deal %d went to %s, want %s", i, wc.Row, want)
		dealt[wc.Row]++
		workIt(w, wc)
		evenly(t, fmt.Sprintf("after deal %d", i), dealt, members, i%8 == 0)
	}
}

// The shortest queue does not choose: the index does, and a member with no
// room is skipped, the index moved past the member dealt to.
func TestTheDealSkipsAFullMemberAndTheQueueDoesNotChoose(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	// m1 and m2 at width 1, m3 at width 2: rooms (DealAhead times the width) of
	// 2, 2 and 4
	for i, m := range []string{"m1", "m2", "m3"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: []int{1, 1, 2}[i]}))
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 8}))
	deal := func(id string) string {
		t.Helper()
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		return w.s.Fleet.Card(WorkCardID(id, 1)).Row
	}
	var got []string
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		got = append(got, deal(id))
	}
	want := []string{"m1", "m2", "m3", "m1"}
	require.Equal(t, want, got, "deals %v, want %v", got, want)
	// m1 two ready (at its room), m2 one, m3 none ready once it takes its card
	// (one working): the index is past m1, so m2, though m3's queue is shorter
	w.must(Take(w.s, TakeReq{As: "m3", Sel: Sel{IDs: []string{"s1-3.w1"}}, Gens: gensOf(w.s, "s1-3.w1")}))
	m := deal("s1-5")
	require.Equal(t, "m2", m, "s1-5 went to %s, want m2 (the index, not m3's shorter queue)", m)
	m = deal("s1-6")
	require.Equal(t, "m3", m, "s1-6 went to %s, want m3", m)
	// past m3: m1 and m2 are at their room, skipped, and m3 (one working, one
	// ready) is below its room of four
	m = deal("s1-7")
	require.Equal(t, "m3", m, "s1-7 went to %s, want m3: m1 and m2 are full", m)
}

// A member down is skipped and its turn is not given to its neighbour twice:
// the index moves past the member dealt to, so the up members share the deals
// evenly.
func TestTheDealSkipsADownMemberEvenly(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	for _, m := range []string{"m1", "m2", "m3", "m4"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 9}))
	var got []string
	for i := 1; i <= 9; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		got = append(got, wc.Row)
		workIt(w, wc)
	}
	want := []string{"m1", "m3", "m4", "m1", "m3", "m4", "m1", "m3", "m4"}
	require.Equal(t, want, got, "deals %v, want %v: m2 is down and skipped, the others in turn", got, want)
}

// 4 free readers, 40 reads asked one primary at a time (two each): every
// reader's count is within one of the others after each ask, equal after each
// full round, and the index is in the store.
func TestTheAskGoesRoundTheReaders(t *testing.T) {
	t.Parallel()
	readers := []string{"reader-a", "reader-b", "reader-c", "reader-d"}
	w := eightIdle(t, readers...)
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		workIt(w, w.s.Fleet.Card(WorkCardID(id, 1)))
	}
	asked := map[string]int{}
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{id}}}))
		for _, rd := range readers {
			if w.s.Readers.Card(ReadCardID(id, 1, rd)) != nil {
				asked[rd]++
			}
		}
		evenly(t, fmt.Sprintf("after ask %d", i), asked, readers, (2*i)%len(readers) == 0)
	}
	last, ok := w.s.Readers.Prop(PropAskIndex)
	require.True(t, ok, "the readers table's ask_index is %q (%v), want reader-d", last, ok)
	require.Equal(t, "reader-d", indexPast(w.s.Readers.Rows(), last), "the readers table's ask_index is %q (%v), want reader-d", last, ok)
}

// The tick's ask (T2) asks round the readers too: one primary in review at a
// time, each worked as it is dealt.
func TestTheTickAskGoesRoundTheReaders(t *testing.T) {
	t.Parallel()
	w := eightIdle(t, "reader-a", "reader-b", "reader-c")
	readers := w.s.Readers.Rows()
	asked := map[string]int{}
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		workIt(w, w.s.Fleet.Card(WorkCardID(id, 1)))
		p := w.part(TickAsk, TickReq{})
		require.Len(t, p.Units, 1, "ask %d: the tick asked %d primaries, want the one in review", i, len(p.Units))
		for _, rd := range readers {
			if w.s.Readers.Card(ReadCardID(id, 1, rd)) != nil {
				asked[rd]++
			}
		}
		evenly(t, fmt.Sprintf("after ask %d", i), asked, readers, (2*i)%len(readers) == 0)
	}
}

// The index is one for the fleet, not one a stream: three streams, one card a
// deal from each in turn, and every member's count is within one of the
// others fleet-wide after each deal, equal after each round of 8.
func TestTheDealGoesRoundTheFleetAcrossStreams(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	for i := 1; i <= 8; i++ {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: fmt.Sprintf("m%d", i)}))
	}
	streams := []string{"s1", "s2", "s3"}
	for _, st := range streams {
		w.must(Add(w.s, AddReq{Stream: st, Count: 8}))
	}
	members := w.s.Fleet.Rows()
	dealt := map[string]int{}
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("%s-%d", streams[i%3], i/3+1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		want := fmt.Sprintf("m%d", i%8+1)
		require.Equal(t, want, wc.Row, "deal %d (%s) went to %s, want %s", i+1, id, wc.Row, want)
		dealt[wc.Row]++
		workIt(w, wc)
		evenly(t, fmt.Sprintf("after deal %d", i+1), dealt, members, (i+1)%8 == 0)
	}
}

// The tick's deal over 10 streams of 3: each run deals the room in stream
// turns, and the members' counts stay within one fleet-wide after every run.
func TestTheTickDealGoesRoundTheFleetOverTenStreams(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 0, 0, "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8")
	for i := 1; i <= 10; i++ {
		w.must(Add(w.s, AddReq{Stream: fmt.Sprintf("s%02d", i), Count: 3}))
	}
	members := w.s.Fleet.Rows()
	dealt := map[string]int{}
	for run := 1; len(w.s.Work.Column(Ready)) > 0; run++ {
		require.LessOrEqual(t, run, 10, "not dealt in 10 runs")
		w.part(TickDeal, TickReq{})
		cs := w.s.Fleet.Column(Ready)
		for _, wc := range cs {
			dealt[wc.Row]++
		}
		for _, wc := range cs {
			workIt(w, wc)
		}
		evenly(t, fmt.Sprintf("after run %d", run), dealt, members, false)
	}
	total := 0
	for _, n := range dealt {
		total += n
	}
	require.Equal(t, 30, total, "dealt %d, want 30", total)
}

// The ask's index is one for the readers: primaries of three streams asked in
// turn keep every reader within one of the others.
func TestTheAskGoesRoundTheReadersAcrossStreams(t *testing.T) {
	t.Parallel()
	readers := []string{"reader-a", "reader-b", "reader-c", "reader-d"}
	w := newWorld(t, readers...)
	for i := 1; i <= 8; i++ {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: fmt.Sprintf("m%d", i)}))
	}
	streams := []string{"s1", "s2", "s3"}
	var ids []string
	for i := 0; i < 18; i++ {
		ids = append(ids, fmt.Sprintf("%s-%d", streams[i%3], i/3+1))
	}
	for _, st := range streams {
		w.must(Add(w.s, AddReq{Stream: st, Count: 6}))
	}
	for _, id := range ids {
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		workIt(w, w.s.Fleet.Card(WorkCardID(id, 1)))
	}
	asked := map[string]int{}
	for i, id := range ids {
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{id}}}))
		for _, rd := range readers {
			if w.s.Readers.Card(ReadCardID(id, 1, rd)) != nil {
				asked[rd]++
			}
		}
		evenly(t, fmt.Sprintf("after ask %d", i+1), asked, readers, (2*(i+1))%len(readers) == 0)
	}
}

// indexPast is the name a rolling index's counter is past (round.go, errata 3
// amendment 5): the name before the one its next scan starts at; "" at 0.
func indexPast(names []string, value string) string {
	order := append([]string(nil), names...)
	slices.Sort(order)
	c := roundCount(order, value)
	if c == 0 || len(order) == 0 {
		return ""
	}
	return order[(c-1)%uint64(len(order))]
}

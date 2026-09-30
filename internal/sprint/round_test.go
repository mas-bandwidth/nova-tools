package sprint

import (
	"fmt"
	"slices"
	"testing"
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
	if hi-lo > 1 || (round && hi != lo) {
		t.Fatalf("%s: %v, want every count within one of the others (equal after a full round)", what, counts)
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
		if want := fmt.Sprintf("m%d", i%8+1); m != want {
			t.Fatalf("deal %d went to %s, want %s: the deals %v", i+1, m, want, order)
		}
	}
	if last, ok := w.s.Fleet.Prop(PropDealIndex); !ok || last != "m6" {
		t.Fatalf("the fleet table's deal_index is %q (%v), want m6", last, ok)
	}
	// every member's done is within one of the others
	done := map[string]int{}
	for _, m := range members {
		done[m] = w.s.Fleet.Count(m, DoneOK)
	}
	evenly(t, "done", done, members, false)
}

// The same through R6: one card ready at a time, dealt by the rule, worked at
// once.
func TestR6GoesRoundTheFleet(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 0, "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8")
	members := f.snap().Fleet.Rows()
	dealt := map[string]int{}
	for i := 1; i <= 24; i++ {
		f.w.must(Add(f.snap(), AddReq{Stream: "s1", Count: 1}))
		f.run(ruleDeal, "deal")
		cs := f.snap().Fleet.Column(Ready)
		if len(cs) != 1 {
			t.Fatalf("deal %d: nothing dealt", i)
		}
		wc := cs[0]
		if want := fmt.Sprintf("m%d", (i-1)%8+1); wc.Row != want {
			t.Fatalf("deal %d went to %s, want %s", i, wc.Row, want)
		}
		dealt[wc.Row]++
		workIt(f.w, wc)
		evenly(t, fmt.Sprintf("after deal %d", i), dealt, members, i%8 == 0)
	}
}

// The shortest queue does not choose: the index does, and a member with no
// room is skipped, the index moved past the member dealt to.
func TestTheDealSkipsAFullMemberAndTheQueueDoesNotChoose(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	// m1 and m2 at width 2, m3 at width 3
	for i, m := range []string{"m1", "m2", "m3"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: []int{2, 2, 3}[i]}))
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
	if want := []string{"m1", "m2", "m3", "m1"}; !slices.Equal(got, want) {
		t.Fatalf("deals %v, want %v", got, want)
	}
	// m1 two ready (at its width), m2 one, m3 none ready once it takes its card
	// (one working): the index is past m1, so m2, though m3's queue is shorter
	w.must(Take(w.s, TakeReq{As: "m3", Sel: Sel{IDs: []string{"s1-3.w1"}}, Gens: gensOf(w.s, "s1-3.w1")}))
	if m := deal("s1-5"); m != "m2" {
		t.Fatalf("s1-5 went to %s, want m2 (the index, not m3's shorter queue)", m)
	}
	if m := deal("s1-6"); m != "m3" {
		t.Fatalf("s1-6 went to %s, want m3", m)
	}
	// past m3: m1 and m2 are at their width, skipped, and m3 (one working, one
	// ready) is below its width of three
	if m := deal("s1-7"); m != "m3" {
		t.Fatalf("s1-7 went to %s, want m3: m1 and m2 are full", m)
	}
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
	if want := []string{"m1", "m3", "m4", "m1", "m3", "m4", "m1", "m3", "m4"}; !slices.Equal(got, want) {
		t.Fatalf("deals %v, want %v: m2 is down and skipped, the others in turn", got, want)
	}
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
	if last, ok := w.s.Readers.Prop(PropAskIndex); !ok || last != "reader-d" {
		t.Fatalf("the readers table's ask_index is %q (%v), want reader-d", last, ok)
	}
}

// R8 asks round the readers too: one primary in review at a time.
func TestR8GoesRoundTheReaders(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 12, 0)
	readers := w.s.Readers.Rows()
	asked := map[string]int{}
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("s1-%d", i)
		rp := rvPlan(w, ruleAsk, "ask:"+id)
		w.must(rp.Plan)
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
		if want := fmt.Sprintf("m%d", i%8+1); wc.Row != want {
			t.Fatalf("deal %d (%s) went to %s, want %s", i+1, id, wc.Row, want)
		}
		dealt[wc.Row]++
		workIt(w, wc)
		evenly(t, fmt.Sprintf("after deal %d", i+1), dealt, members, (i+1)%8 == 0)
	}
}

// R6 over 10 streams of 3: each run deals the room in stream turns, and the
// members' counts stay within one fleet-wide after every run.
func TestR6GoesRoundTheFleetOverTenStreams(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 0, "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8")
	for i := 1; i <= 10; i++ {
		f.w.must(Add(f.snap(), AddReq{Stream: fmt.Sprintf("s%02d", i), Count: 3}))
	}
	members := f.snap().Fleet.Rows()
	dealt := map[string]int{}
	for run := 1; len(f.snap().Work.Column(Ready)) > 0; run++ {
		if run > 10 {
			t.Fatalf("not dealt in 10 runs")
		}
		f.run(ruleDeal, "deal")
		cs := f.snap().Fleet.Column(Ready)
		for _, wc := range cs {
			dealt[wc.Row]++
		}
		for _, wc := range cs {
			workIt(f.w, wc)
		}
		evenly(t, fmt.Sprintf("after run %d", run), dealt, members, false)
	}
	total := 0
	for _, n := range dealt {
		total += n
	}
	if total != 30 {
		t.Fatalf("dealt %d, want 30", total)
	}
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

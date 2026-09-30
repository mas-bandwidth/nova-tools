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
// index is in the store, on the stream's control card.
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
	if last, seq := roundAt(w.s, FieldDealSeq, FieldDealLast); last != "m6" || seq != 30 {
		t.Fatalf("the index in the store is past %q at sequence %d, want past m6 at 30", last, seq)
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
	for _, m := range []string{"m1", "m2", "m3"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
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
	// m1 two ready (full), m2 one, m3 none once it takes its card: the index is
	// past m1, so m2, though m3's queue is shorter
	w.must(Take(w.s, TakeReq{As: "m3", Sel: Sel{IDs: []string{"s1-3.w1"}}, Gens: gensOf(w.s, "s1-3.w1")}))
	if m := deal("s1-5"); m != "m2" {
		t.Fatalf("s1-5 went to %s, want m2 (the index, not m3's shorter queue)", m)
	}
	if m := deal("s1-6"); m != "m3" {
		t.Fatalf("s1-6 went to %s, want m3", m)
	}
	// past m3: m1 and m2 are full, skipped, and m3 has room
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
	if last, seq := roundAt(w.s, FieldAskSeq, FieldAskLast); last != "reader-d" || seq != 20 {
		t.Fatalf("the index in the store is past %q at sequence %d, want past reader-d at 20", last, seq)
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

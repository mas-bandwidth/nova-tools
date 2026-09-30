package sprint

import (
	"slices"
	"testing"
)

// The streams take turns from the work table's rolling stream index (errata 3
// amendment 10, round.go): every step that takes cards across the streams
// takes one of each stream in turn from the stream past the index, a stream
// with nothing to take costing no turn, and moves the index past the stream of
// the last card it took.

// streamOfTaken is the stream of each primary a plan moved, in its order.
func streamOfTaken(s *Snapshot, p Plan) []string {
	var out []string
	for _, u := range p.Units {
		if c := s.Work.Placed(u.Key); c != nil {
			out = append(out, c.Row)
		}
	}
	return out
}

// dealOne deals one ready primary and says the stream it came from and the
// index it left.
func dealOne(t *testing.T, f *fleetT) (string, string) {
	t.Helper()
	s := f.snap()
	p := f.w.must(Deal(s, DealReq{Sel: Sel{Limit: 1}, Who: "coordinator"}))
	got := streamOfTaken(s, p)
	if len(got) != 1 {
		t.Fatalf("a deal of one moved %v", got)
	}
	idx, _ := f.snap().Work.Prop(PropStreamIndex)
	return got[0], idx
}

func TestStreamTurnsStartPastTheIndexAndSkipAStreamWithNone(t *testing.T) {
	t.Parallel()
	var cards []*Card
	for _, st := range []string{"s1", "s3"} {
		for i := 1; i <= 3; i++ {
			cards = append(cards, &Card{ID: st + "-" + itoa(i), Row: st, Score: float64(i)})
		}
	}
	rows := []string{"s1", "s2", "s3"}
	for last, want := range map[string][]string{
		"":   {"s1", "s3", "s1", "s3", "s1", "s3"},
		"s1": {"s3", "s1", "s3", "s1", "s3", "s1"}, // s2 has none: it costs no turn
		"s2": {"s3", "s1", "s3", "s1", "s3", "s1"},
		"s3": {"s1", "s3", "s1", "s3", "s1", "s3"},
	} {
		var got []string
		for _, c := range streamTurns(cards, newRound(rows, last)) {
			got = append(got, c.Row)
		}
		if !slices.Equal(got, want) {
			t.Errorf("past %q: %v, want %v", last, got, want)
		}
	}
}

// Three streams, one with nothing ready: the deal alternates the two with
// cards, and when the third has cards it joins the rotation at its turn. The
// index is the work table's, read back after every step.
func TestTheDealAlternatesTheStreamsWithCardsAndAnEmptyOneJoinsAtItsTurn(t *testing.T) {
	t.Parallel()
	f := newFleetW(t, 5, 64, "m1", "m2")
	f.w.must(Add(f.snap(), AddReq{Stream: "s2", Count: 3, Needs: []string{"s1-5"}}))
	f.w.must(Add(f.snap(), AddReq{Stream: "s3", Count: 5}))
	var got []string
	for i := 0; i < 4; i++ {
		st, idx := dealOne(t, f)
		if idx != st {
			t.Fatalf("the index is %q after a card of %s", idx, st)
		}
		got = append(got, st)
	}
	if want := []string{"s1", "s3", "s1", "s3"}; !slices.Equal(got, want) {
		t.Fatalf("with s2 waiting the deal goes %v, want %v", got, want)
	}
	// s2's cards are ready now: past s3 comes s1, then s2 at its turn
	for _, id := range []string{"s2-1", "s2-2", "s2-3"} {
		f.place(f.snap().Work, id, "s2", Ready)
	}
	got = nil
	for i := 0; i < 6; i++ {
		st, _ := dealOne(t, f)
		got = append(got, st)
	}
	if want := []string{"s1", "s2", "s3", "s1", "s2", "s3"}; !slices.Equal(got, want) {
		t.Fatalf("with s2 ready the deal goes %v, want %v", got, want)
	}
}

// The ask takes the primaries in review in stream turns from its own index on
// the work table, and the deal's index is left where the deal left it: one
// step's move never resets another's rotation.
func TestTheAskTakesTheStreamsInTurnFromItsOwnIndex(t *testing.T) {
	t.Parallel()
	f := newFleetW(t, 4, 64, "m1")
	f.snap().Readers.SetRows(append(f.snap().Readers.Rows(), "reader-b"))
	f.w.must(Add(f.snap(), AddReq{Stream: "s2", Count: 4}))
	f.w.must(Add(f.snap(), AddReq{Stream: "s3", Count: 4}))
	if st, _ := dealOne(t, f); st != "s1" {
		t.Fatalf("the first deal takes s1, took %s", st)
	}
	for _, st := range []string{"s1", "s2", "s3"} {
		for i := 2; i <= 4; i++ {
			f.place(f.snap().Work, st+"-"+itoa(i), st, Review)
		}
	}
	s := f.snap()
	p := f.w.must(Ask(s, AskReq{Sel: Sel{Limit: 4}, Who: "coordinator"}))
	if got, want := streamOfTaken(s, p), []string{"s1", "s2", "s3", "s1"}; !slices.Equal(got, want) {
		t.Fatalf("the ask takes %v, want %v", got, want)
	}
	if idx, _ := f.snap().Readers.Prop(PropAskStreamIndex); idx != "s1" {
		t.Fatalf("the ask's index is %q, want s1", idx)
	}
	// the tick's ask asks every primary left, in turns past s1
	s = f.snap()
	tp, due := TickAsk(s, TickReq{})
	if got, want := streamOfTaken(s, tp), []string{"s2", "s3", "s1", "s2", "s3"}; due != 0 || !slices.Equal(got, want) {
		t.Fatalf("the tick's ask past s1 takes %v (due %d), want %v", got, due, want)
	}
	f.w.must(tp)
	if idx, _ := f.snap().Work.Prop(PropStreamIndex); idx != "s1" {
		t.Fatalf("the deal's index is %q after the asks, want s1 where the deal left it", idx)
	}
	if st, _ := dealOne(t, f); st != "s2" {
		t.Fatalf("the deal after the asks takes %s, want s2, past its own index", st)
	}
}

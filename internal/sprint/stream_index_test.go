package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
func dealOne(t *testing.T, w *world) (string, string) {
	t.Helper()
	s := w.s
	p := w.must(Deal(s, DealReq{Sel: Sel{Limit: 1}, Who: "coordinator"}))
	got := streamOfTaken(s, p)
	require.Len(t, got, 1, "a deal of one moved %v", got)
	idx, _ := w.s.Work.Prop(PropStreamIndex)
	return got[0], indexPast(w.s.Work.Rows(), idx)
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
		assert.Equal(t, want, got, "past %q: %v, want %v", last, got, want)
	}
}

// Three streams, one with nothing ready: the deal alternates the two with
// cards, and when the third has cards it joins the rotation at its turn. The
// index is the work table's, read back after every step.
func TestTheDealAlternatesTheStreamsWithCardsAndAnEmptyOneJoinsAtItsTurn(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 5, 64, "m1", "m2")
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 3, Needs: []string{"s1-5"}}))
	w.must(Add(w.s, AddReq{Stream: "s3", Count: 5}))
	var got []string
	for i := 0; i < 4; i++ {
		st, idx := dealOne(t, w)
		require.Equal(t, st, idx, "the index is %q after a card of %s", idx, st)
		got = append(got, st)
	}
	want := []string{"s1", "s3", "s1", "s3"}
	require.Equal(t, want, got, "with s2 waiting the deal goes %v, want %v", got, want)
	// s2's cards are ready now: past s3 comes s1, then s2 at its turn
	for _, id := range []string{"s2-1", "s2-2", "s2-3"} {
		w.place(w.s.Work, id, "s2", Ready)
	}
	got = nil
	for i := 0; i < 6; i++ {
		st, _ := dealOne(t, w)
		got = append(got, st)
	}
	want = []string{"s1", "s2", "s3", "s1", "s2", "s3"}
	require.Equal(t, want, got, "with s2 ready the deal goes %v, want %v", got, want)
}

// The ask takes the primaries in review in stream turns from its own index on
// the work table, and the deal's index is left where the deal left it: one
// step's move never resets another's rotation.
func TestTheAskTakesTheStreamsInTurnFromItsOwnIndex(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 4, 64, "m1")
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-b"))
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 4}))
	w.must(Add(w.s, AddReq{Stream: "s3", Count: 4}))
	first, _ := dealOne(t, w)
	require.Equal(t, "s1", first, "the first deal takes s1, took %s", first)
	for _, st := range []string{"s1", "s2", "s3"} {
		for i := 2; i <= 4; i++ {
			w.place(w.s.Work, st+"-"+itoa(i), st, Review)
		}
	}
	s := w.s
	p := w.must(Ask(s, AskReq{Sel: Sel{Limit: 4}, Who: "coordinator"}))
	got, want := streamOfTaken(s, p), []string{"s1", "s2", "s3", "s1"}
	require.Equal(t, want, got, "the ask takes %v, want %v", got, want)
	idx, _ := w.s.Readers.Prop(PropAskStreamIndex)
	require.Equal(t, "s1", indexPast(w.s.Work.Rows(), idx), "the ask's index is %q, want s1", idx)
	// the tick's ask asks every primary left, in turns past s1
	s = w.s
	tp, due := TickAsk(s, TickReq{})
	got, want = streamOfTaken(s, tp), []string{"s2", "s3", "s1", "s2", "s3"}
	require.Zero(t, due, "the tick's ask past s1 takes %v (due %d), want %v", got, due, want)
	require.Equal(t, want, got, "the tick's ask past s1 takes %v (due %d), want %v", got, due, want)
	w.must(tp)
	idx, _ = w.s.Work.Prop(PropStreamIndex)
	require.Equal(t, "s1", indexPast(w.s.Work.Rows(), idx), "the deal's index is %q after the asks, want s1 where the deal left it", idx)
	next, _ := dealOne(t, w)
	require.Equal(t, "s2", next, "the deal after the asks takes %s, want s2, past its own index", next)
}

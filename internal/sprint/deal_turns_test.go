package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deal works every stream in parallel (front(s) per stream): one
// card from each stream in turn, never one stream's backlog before another's
// first card.

// streamsRoom is the room of streamsOf30's fleet: eight members of width 2,
// each dealt DealAhead times it.
const streamsRoom = 8 * DealAhead * 2

// streamsOf30 is the fleet harness with 8 up members of width 2 and three
// streams of 30 ready primaries, s1 added first (the lowest scores).
func streamsOf30(t *testing.T) *world {
	t.Helper()
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	w := fleetWorld(t, 30, 2, members...) // at width 2 the room is streamsRoom, 32
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 30}))
	w.must(Add(w.s, AddReq{Stream: "s3", Count: 30}))
	return w
}

// workingBy is each stream's count of primaries in working.
func workingBy(s *Snapshot, streams ...string) map[string]int {
	out := map[string]int{}
	for _, st := range streams {
		out[st] = s.Work.Count(st, Working)
	}
	return out
}

// spread fails unless every stream has a card working and the counts differ by
// at most one.
func spread(t *testing.T, got map[string]int) {
	t.Helper()
	lo, hi := -1, 0
	for _, n := range got {
		if lo < 0 || n < lo {
			lo = n
		}
		hi = max(hi, n)
	}
	require.GreaterOrEqual(t, lo, 1, "working by stream %v: every stream works, within one of each other", got)
	require.LessOrEqual(t, hi-lo, 1, "working by stream %v: every stream works, within one of each other", got)
}

// lowestWorking fails unless the stream's working primaries are its lowest
// scored: the order within a stream is by score.
func lowestWorking(t *testing.T, s *Snapshot, stream string) {
	t.Helper()
	var all []*Card
	for _, c := range s.Work.Column(States...) {
		if c.Row == stream && !IsSentinel(c) {
			all = append(all, c)
		}
	}
	SortCards(all)
	n := s.Work.Count(stream, Working)
	for i, c := range all {
		require.Equal(t, i < n, c.Col == Working, "%s: %s is %s at place %d of its stream; the %d lowest scored work", stream, c.ID, c.Col, i, n)
	}
}

func TestDealWorksEveryStreamInParallel(t *testing.T) {
	t.Parallel()
	w := streamsOf30(t)
	w.part(TickDeal, TickReq{})
	got := workingBy(w.s, "s1", "s2", "s3")
	require.Equal(t, streamsRoom, got["s1"]+got["s2"]+got["s3"], "working %v, want the room of %d dealt", got, streamsRoom)
	spread(t, got)
	for _, st := range []string{"s1", "s2", "s3"} {
		lowestWorking(t, w.s, st)
	}
}

func TestDealSkipsAStreamWithNoReadyCard(t *testing.T) {
	t.Parallel()
	w := streamsOf30(t)
	for i := 1; i <= 30; i++ {
		w.place(w.s.Work, fmt.Sprintf("s2-%d", i), "s2", Waiting)
	}
	w.part(TickDeal, TickReq{})
	got := workingBy(w.s, "s1", "s2", "s3")
	require.Zero(t, got["s2"], "working %v, want s2 skipped and the room of %d split evenly", got, streamsRoom)
	require.Equal(t, streamsRoom/2, got["s1"], "working %v, want s2 skipped and the room of %d split evenly", got, streamsRoom)
	require.Equal(t, streamsRoom/2, got["s3"], "working %v, want s2 skipped and the room of %d split evenly", got, streamsRoom)
}

func TestTickDealWorksEveryStreamInParallel(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	for i := 1; i <= 8; i++ {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: fmt.Sprintf("m%d", i), Width: 2}))
	}
	for _, st := range []string{"s1", "s2", "s3"} {
		w.must(Add(w.s, AddReq{Stream: st, Count: 30}))
	}
	p, _ := TickDeal(w.s, TickReq{})
	w.must(p)
	got := workingBy(w.s, "s1", "s2", "s3")
	require.Equal(t, streamsRoom, got["s1"]+got["s2"]+got["s3"], "working %v, want the room of %d dealt", got, streamsRoom)
	spread(t, got)
	for _, st := range []string{"s1", "s2", "s3"} {
		lowestWorking(t, w.s, st)
	}
}

func TestDealTurnsOrder(t *testing.T) {
	t.Parallel()
	card := func(id, row string, score float64) *Card { return &Card{ID: id, Row: row, Score: score} }
	cards := []*Card{
		card("a3", "a", 3), card("a1", "a", 1), card("a2", "a", 2), card("a4", "a", 4),
		card("c2", "c", 20), card("c1", "c", 10),
		card("b1", "b", 100),
	}
	var ids []string
	for _, c := range dealTurns(cards, []string{"a", "empty", "b", "c"}) {
		ids = append(ids, c.ID)
	}
	want := []string{"a1", "b1", "c1", "a2", "c2", "a3", "a4"}
	require.Equal(t, want, ids, "turns %v, want %v (a stream at a time in the given order, by score within, the empty one skipped)", ids, want)
	// a stream not listed takes its turn after the listed ones
	ids = ids[:0]
	for _, c := range dealTurns(cards, []string{"c"}) {
		ids = append(ids, c.ID)
	}
	want = []string{"c1", "a1", "b1", "c2", "a2", "a3", "a4"}
	require.Equal(t, want, ids, "turns %v, want %v", ids, want)
}

// A card with a thousand needs is held by them, and says so in one short line:
// the count and the first few, never every id.
func TestHeldNeedsPreview(t *testing.T) {
	t.Parallel()
	w := setup(t, 1000)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2", "s1-3", "s1-4"}}}))
	needs := make([]string, 1000)
	for i := range needs {
		needs[i] = fmt.Sprintf("s1-%d", i+1)
	}
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"big"}, Needs: needs}))
	hd := mustHold(t, running(w), "big", HeldByWaiting)
	require.Less(t, len(hd.Why), 300, "%d bytes: %s", len(hd.Why), hd.Why)
	require.Contains(t, hd.Why, "needs 1000 of 1000 still open: s1-1 (", "%d bytes: %s", len(hd.Why), hd.Why)
	require.Contains(t, hd.Why, "... and 992 more", "%d bytes: %s", len(hd.Why), hd.Why)
}

func TestPreview(t *testing.T) {
	t.Parallel()
	few := []string{"a", "b", "c"}
	got := Preview(few, ",")
	require.Equal(t, "a,b,c", got, "few: %q", got)
	many := make([]string, 1000)
	for i := range many {
		many[i] = fmt.Sprintf("a-%d", i)
	}
	got = Preview(many, ", ")
	want := "a-0, a-1, a-2, a-3, a-4, a-5, a-6, a-7, ... and 992 more"
	require.Equal(t, want, got, "many: %q, want %q", got, want)
}

// A card the deal leaves waiting says how many ready cards are ahead of it in
// the deal's order (held.go's dealTurn, the stream turns from the deal's
// index): after the tick's deal of three streams of 30 to 8 members of width 2
// (DealAhead times two each: thirty-two cards, the last of s2, so the index is
// past s2), the third stream's next card has none ahead, the first's one, the
// second's two.
func TestAHeldReadyCardCountsWhatIsAheadInTheDealsOrder(t *testing.T) {
	t.Parallel()
	w := streamsOf30(t)
	w.part(TickDeal, TickReq{})
	h := HeldState{Snap: w.s, Running: true}
	for id, ahead := range map[string]int{"s3-11": 0, "s1-12": 1, "s2-12": 2, "s3-12": 3, "s1-13": 4, "s2-13": 5} {
		hd := Holder(h, h.Snap.Now, id)
		want := fmt.Sprintf("0 free, %d ready ahead of it", ahead)
		assert.Contains(t, hd.String(), want, "%s: %s, want %q", id, hd, want)
	}
}

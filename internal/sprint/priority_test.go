package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A priority is a group's, never a card's (docs/SPEC-SPRINT.md, the deal: priority): the
// deal takes the groups in descending level and, inside a level, the streams' turns as
// before; within a stream the order stays the stream's own.

// leveled is a fleet of two members of width 1 (a room of 2*DealAhead) and streams s1, s2
// of 20 ready primaries and rel of 3, with rel at level 2 and s2 at level 1.
func leveled(t *testing.T) *world {
	t.Helper()
	w := fleetWorld(t, 20, 1, "m1", "m2")
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 20}))
	w.must(Add(w.s, AddReq{Stream: "rel", Count: 3}))
	w.must(PrioritySet(w.s, PriorityReq{Streams: []string{"rel"}, Level: 2, Who: "coordinator"}))
	w.must(PrioritySet(w.s, PriorityReq{Streams: []string{"s2"}, Level: 1, Who: "coordinator"}))
	return w
}

func TestAHigherLevelGroupIsDealtBeforeALowerOneWithoutReorderingAStream(t *testing.T) {
	t.Parallel()
	room := 2 * DealAhead

	t.Run("the machines take the highest level first, then the next", func(t *testing.T) {
		t.Parallel()
		w := leveled(t)
		require.Equal(t, 2, StreamLevel(w.s, "rel"))
		require.Equal(t, 1, StreamLevel(w.s, "s2"))
		require.Equal(t, 0, StreamLevel(w.s, "s1"), "a stream no priority names is at level 0")
		w.part(TickDeal, TickReq{})
		got := workingBy(w.s, "rel", "s2", "s1")
		assert.Equal(t, map[string]int{"rel": 3, "s2": room - 3, "s1": 0}, got,
			"every card of level 2, then level 1's, and level 0 waits while the room is spent")
		for _, st := range []string{"rel", "s2", "s1"} {
			lowestWorking(t, w.s, st) // rank unchanged: each stream's lowest scored work
		}
	})

	t.Run("inside a level the streams still take turns", func(t *testing.T) {
		t.Parallel()
		w := fleetWorld(t, 20, 1, "m1", "m2")
		w.must(Add(w.s, AddReq{Stream: "s2", Count: 20}))
		w.must(Add(w.s, AddReq{Stream: "s3", Count: 20}))
		w.must(PrioritySet(w.s, PriorityReq{Streams: []string{"s1", "s3"}, Level: 1, Who: "coordinator"}))
		w.part(TickDeal, TickReq{})
		assert.Equal(t, map[string]int{"s1": room / 2, "s3": room / 2, "s2": 0}, workingBy(w.s, "s1", "s2", "s3"))
		lowestWorking(t, w.s, "s1")
		lowestWorking(t, w.s, "s3")
	})

	t.Run("a group with nothing eligible yields to the next", func(t *testing.T) {
		t.Parallel()
		w := leveled(t)
		for i := 1; i <= 3; i++ {
			w.place(w.s.Work, fmt.Sprintf("rel-%d", i), "rel", Waiting)
		}
		w.part(TickDeal, TickReq{})
		assert.Equal(t, map[string]int{"rel": 0, "s2": room, "s1": 0}, workingBy(w.s, "rel", "s2", "s1"))
		lowestWorking(t, w.s, "s2")
	})

	t.Run("a friend's room goes to the higher level first", func(t *testing.T) {
		t.Parallel()
		// the case of 2026-10-06: two cards for one friend, first in their stream, sat
		// undealt while a stream of many filled her row by turns
		w := newWorld(t, "reader-a")
		var big, rel []CardAdd
		for i := 1; i <= 10; i++ {
			big = append(big, CardAdd{ID: fmt.Sprintf("big-%d", i), Brief: friendBrief("friend")})
		}
		for i := 1; i <= 2; i++ {
			rel = append(rel, CardAdd{ID: fmt.Sprintf("zrel-%d", i), Brief: friendBrief("friend stella")})
		}
		w.must(Add(w.s, AddReq{Stream: "big", Cards: big}))
		w.must(Add(w.s, AddReq{Stream: "zrel", Cards: rel}))
		stella := FriendSeat{Name: "stella", Width: 1, Status: Up, Class: "flash,pro"}

		before := newWorld(t, "reader-a")
		before.must(Add(before.s, AddReq{Stream: "big", Cards: big}))
		before.must(Add(before.s, AddReq{Stream: "zrel", Cards: rel}))
		dealWith(before, stella)
		assert.Equal(t, 1, before.s.Fleet.Count(FriendRow("stella"), Ready)-countOn(before.s, FriendRow("stella"), "zrel"),
			"with no level the turns give her one card of each stream")

		w.must(PrioritySet(w.s, PriorityReq{Streams: []string{"zrel"}, Level: 1, Who: "coordinator"}))
		dealWith(w, stella)
		assert.Equal(t, 2, countOn(w.s, FriendRow("stella"), "zrel"), "her room goes to the level-1 group first")
	})

	t.Run("a release sets every stream of it, and a bad name is refused", func(t *testing.T) {
		t.Parallel()
		w := fleetWorld(t, 2, 1, "m1")
		w.must(Add(w.s, AddReq{Stream: "s2", Count: 2}))
		w.must(Add(w.s, AddReq{Stream: "s3", Count: 2}))
		w.must(Set(w.s, SetReq{Streams: []string{"s1", "s3"}, Release: "v1.1.0", Who: "coordinator"}))
		w.must(PrioritySet(w.s, PriorityReq{Release: "v1.1.0", Level: 3, Who: "coordinator"}))
		assert.Equal(t, 3, StreamLevel(w.s, "s1"))
		assert.Equal(t, 0, StreamLevel(w.s, "s2"))
		assert.Equal(t, 3, StreamLevel(w.s, "s3"))
		w.must(PrioritySet(w.s, PriorityReq{Streams: []string{"s3"}, Level: 0, Who: "coordinator"}))
		assert.Equal(t, 0, StreamLevel(w.s, "s3"), "level 0 takes the level off")
		assert.Empty(t, w.s.StreamCtl("s3").F(FieldLevel))

		for _, r := range []PriorityReq{
			{Streams: []string{"nope"}, Level: 1},
			{Release: "v9", Level: 1},
			{Level: 1},
			{Streams: []string{"s1"}, Release: "v1.1.0", Level: 1},
			{Streams: []string{"s1"}, Level: -1},
		} {
			p := PrioritySet(w.s, r)
			assert.NotEmpty(t, p.Refused, "%+v is refused", r)
			assert.Empty(t, p.Units, "%+v changes nothing", r)
		}
	})
}

// countOn is the work cards of the stream on the row, ready or working.
func countOn(s *Snapshot, row, stream string) int {
	n := 0
	for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...) {
		if c.F("stream") == stream {
			n++
		}
	}
	return n
}

func TestWhyNamesWhatKeepsAReadyCardFromDealing(t *testing.T) {
	t.Parallel()
	w := leveled(t)
	stella := FriendSeat{Name: "stella", Width: 1, Status: Up, Class: "pro"}
	lines, refused := WhyNotDealt(w.s, "s1-1", []FriendSeat{stella, {Name: "amy", Width: 2, Status: Down, Class: "flash"}})
	require.Empty(t, refused)
	by := map[string]string{}
	var keys []string
	for _, l := range lines {
		by[l.Key] = l.Text
		keys = append(keys, l.Key)
	}
	assert.Equal(t, []string{"group", "order", "hold", "who", "tier", "friends", "machines", "bound"}, keys, "one line each")
	assert.Contains(t, by["group"], "level 0 (stream s1)")
	assert.Contains(t, by["group"], "rel level 2 (3 ready)")
	assert.Contains(t, by["group"], "s2 level 1 (20 ready)")
	assert.Contains(t, by["order"], "23 ready ahead of it", "every card of the two groups ahead")
	assert.Contains(t, by["hold"], "not held")
	assert.Contains(t, by["who"], "no WHO line")
	assert.Contains(t, by["tier"], "flash")
	assert.Contains(t, by["friends"], "stella not her tier, room 2 of 2")
	assert.Contains(t, by["friends"], "amy down")
	assert.Contains(t, by["machines"], "2 up, room 4 free of 4")
	assert.Equal(t, "none", by["bound"])

	lines, _ = WhyNotDealt(w.s, "rel-1", nil)
	assert.Contains(t, lines[0].Text, "no group of a higher level has a ready card")
	assert.Contains(t, lines[1].Text, "0 ready ahead of it")

	w.part(TickDeal, TickReq{})
	lines, _ = WhyNotDealt(w.s, "rel-1", nil)
	require.Len(t, lines, 1)
	assert.Equal(t, "state", lines[0].Key)
	assert.Contains(t, lines[0].Text, "not ready")
	_, refused = WhyNotDealt(w.s, "nope-1", nil)
	assert.Contains(t, refused, "no card nope-1")
}

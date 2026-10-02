package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The coordinator's edits of a STOPPED sprint's unstarted primaries (the
// owner, 2026-10-01: "What other things should you be able to do to mutate a
// stopped sprint" / "I don't want you manually hopping in and working around
// it and doing manual stuff."): refused on a RUNNING machine, and for a card
// that has started, one case per state.

// editWorld is stream a with a-1 ready and a-2 waiting on it.
func editWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-1"}, Brief: "old one", Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-2"}, Needs: []string{"a-1"}, Brief: "old two", Who: "coordinator"}))
	require.Equal(t, Ready, w.s.Work.Placed("a-1").Col)
	require.Equal(t, Waiting, w.s.Work.Placed("a-2").Col)
	return w
}

// startedCases is a started primary in each state an edit refuses: dealt once
// and back in ready, working, in review, merging and landed.
var startedCases = []struct {
	name, col string
	attempt   string
}{
	{"ready with an attempt dealt", Ready, "1"},
	{"working", Working, "1"},
	{"review", Review, "1"},
	{"merging", Merging, "1"},
	{"landed", Landed, "1"},
}

func TestBriefReplacesTheBriefOfAnUnstartedPrimary(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a-1", "a-2"} {
		w := editWorld(t)
		before := *w.s.Work.Placed(id)
		w.must(Brief(w.s, BriefReq{ID: id, Brief: "new brief", Who: "coordinator"}))
		c := w.s.Work.Placed(id)
		assert.Equal(t, "new brief", c.F("brief"), id)
		assert.Equal(t, before.Row, c.Row, id)
		assert.Equal(t, before.Col, c.Col, id)
		assert.Equal(t, before.Score, c.Score, id)
		assert.Equal(t, before.Fields["needs"], c.F("needs"), id)
	}
}

func TestBriefIsRefusedOnARunningMachine(t *testing.T) {
	t.Parallel()
	w := editWorld(t)
	w.s.Running = true
	p := Brief(w.s, BriefReq{ID: "a-1", Brief: "new", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "the machine is RUNNING")
	assert.Contains(t, p.Refused[0].Why, "run: nova-sprint stop")
}

func TestBriefIsRefusedForAStartedCard(t *testing.T) {
	t.Parallel()
	for _, tc := range startedCases {
		w := editWorld(t)
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		p := Brief(w.s, BriefReq{ID: "a-1", Brief: "new", Who: "coordinator"})
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, "a-1 is "+tc.col, tc.name)
		assert.Contains(t, p.Refused[0].Why, "keeps its brief", tc.name)
	}
}

func TestBriefIsRefusedForASentinelAndAnUnknownCard(t *testing.T) {
	t.Parallel()
	w := editWorld(t)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Brief(w.s, BriefReq{ID: "a-stop", Brief: "new", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a-stop is a sentinel, not a primary")
	p = Brief(w.s, BriefReq{ID: "zz", Brief: "new", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "no primary zz on the work table")
}

// moveWorld is editWorld with stream b: b-1 ready, and b-2 waiting on a-1, a
// need across the streams.
func moveWorld(t *testing.T) *world {
	t.Helper()
	w := editWorld(t)
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-1"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-2"}, Needs: []string{"a-1"}, Who: "coordinator"}))
	return w
}

// move is a move as the store applies it: held to the lifecycle (Lawful).
func move(w *world, r MoveReq) Plan {
	w.t.Helper()
	r.Who = "coordinator"
	return w.must(Lawful(MoveCards(w.s, r)))
}

func TestMoveTakesUnstartedPrimariesToTheEndOfAnotherStream(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	a2 := *w.s.Work.Placed("a-2")
	move(w, MoveReq{IDs: []string{"a-1", "a-2"}, Stream: "b"})
	a1, a2now := w.s.Work.Placed("a-1"), w.s.Work.Placed("a-2")
	assert.Equal(t, "b", a1.Row)
	assert.Equal(t, "b", a1.F("stream"))
	assert.Equal(t, Ready, a1.Col)
	assert.Equal(t, "b", a2now.Row)
	assert.Equal(t, Waiting, a2now.Col, "a-2 still waits on a-1")
	assert.Equal(t, "a-1", a2now.F("needs"), "a need is by id: it holds")
	assert.Equal(t, a2.Fields["brief"], a2now.F("brief"))
	assert.Equal(t, a2.Fields["admitted"], a2now.F("admitted"))
	for _, id := range []string{"b-1", "b-2"} {
		assert.Less(t, w.s.Work.Placed(id).Score, a1.Score, "moved to the end of b's line, after %s", id)
	}
	assert.Less(t, a1.Score, a2now.Score)
	b2 := w.s.Work.Placed("b-2")
	assert.Equal(t, Waiting, b2.Col)
	assert.Equal(t, "a-1", b2.F("needs"), "a need naming the moved card holds")
	w.clean("after the move")
}

func TestMoveMakesANewStreamAsAddDoes(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "c"})
	assert.True(t, w.s.Work.HasRow("c"))
	assert.True(t, w.s.Merge.HasRow("c"))
	require.NotNil(t, w.s.StreamCtl("c"))
	assert.Equal(t, StreamWaiting, w.s.StreamCtl("c").F("state"))
	assert.Equal(t, "c", w.s.Work.Placed("a-1").Row)
	w.clean("after the move")
}

func TestMovePlacesTheCardsAsAddDoes(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "b", Before: "b-1"})
	assert.Less(t, w.s.Work.Placed("a-1").Score, w.s.Work.Placed("b-1").Score)
	w = moveWorld(t)
	score := 0.25
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "b", Score: &score})
	assert.Equal(t, 0.25, w.s.Work.Placed("a-1").Score)
}

func TestMoveIsRefusedOnARunningMachine(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.s.Running = true
	p := MoveCards(w.s, MoveReq{IDs: []string{"a-1", "a-2"}, Stream: "b", Who: "coordinator"})
	require.Len(t, p.Refused, 2)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "the machine is RUNNING")
	assert.Contains(t, p.Refused[0].Why, "run: nova-sprint stop")
}

func TestMoveIsRefusedForAStartedCard(t *testing.T) {
	t.Parallel()
	for _, tc := range startedCases {
		w := moveWorld(t)
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		p := MoveCards(w.s, MoveReq{IDs: []string{"a-1"}, Stream: "b", Who: "coordinator"})
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, "a-1 is "+tc.col, tc.name)
		assert.Contains(t, p.Refused[0].Why, "keeps its stream", tc.name)
	}
}

func TestMoveIsRefusedIntoItsOwnStreamAndForASentinel(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-stop"}, Sentinel: true, Who: "coordinator"}))
	p := MoveCards(w.s, MoveReq{IDs: []string{"a-1", "a-stop"}, Stream: "a", Who: "coordinator"})
	require.Len(t, p.Refused, 2)
	assert.Contains(t, p.Refused[0].Why, "a-1 is in stream a already")
	assert.Contains(t, p.Refused[1].Why, "a-stop is a sentinel, not a primary")
}

// A ready card the destination would put behind a sentinel is refused by the
// lifecycle: ready -> waiting is only the effect of inserting a sentinel.
func TestMoveBehindASentinelOfAReadyCardIsRefusedByTheLifecycle(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c-1"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Lawful(MoveCards(w.s, MoveReq{IDs: []string{"a-1"}, Stream: "c", Who: "coordinator"}))
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "ready -> waiting only as the effect of inserting a sentinel")
	// in front of the stop it moves, ready
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "c", Before: "c-stop"})
	assert.Equal(t, Ready, w.s.Work.Placed("a-1").Col)
	assert.Equal(t, "c", w.s.Work.Placed("a-1").Row)
	w.clean("after the move")
}

// A move that would close a cycle of needs through a sentinel is refused,
// as add refuses one: b-2 needs a-1, and behind b's stop a-1 would need it.
func TestMoveThatClosesACycleIsRefused(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Lawful(MoveCards(w.s, MoveReq{IDs: []string{"a-1"}, Stream: "b", Who: "coordinator"}))
	require.Len(t, p.Refused, 1)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "the needs would make a cycle")
}

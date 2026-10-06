package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTick is the tick's start window and its deal with these friends, in the tick's
// order: the seats read with their started lanes first (StartedSeats), then the start
// window part (TickFriendStart), then the deal; both applied. It answers the start
// window's plan.
func startTick(w *world, seats ...FriendSeat) Plan {
	w.t.Helper()
	read := StartedSeats(w.s, seats)
	sp, _ := TickFriendStart(w.s, TickReq{Friends: read})
	w.must(sp)
	dealWith(w, read...)
	return sp
}

// notStartedNotes is the notes that say a card went back to the pool unstarted.
func notStartedNotes(w *world) []Note {
	var out []Note
	for _, n := range w.notes {
		if strings.Contains(n.What, "back to the pool") {
			out = append(out, n)
		}
	}
	return out
}

// The night of 2026-10-05: a friend at width 8 in batch mode held seven heavy cards for six
// hours and started none. A friend is dealt what her session starts (docs/SPEC-SPRINT.md
// section 1, the start window): a card in her lane that her beat does not name running
// within the start window goes back to the pool with a note, and her row's effective width
// for the next deals is the cards she has started, rising as she starts; her width is the
// ceiling.
func TestAFriendIsDealtOnlyWhatHerSessionStarts(t *testing.T) {
	t.Parallel()
	zhi := FriendRow("zhi")
	seat := func(running ...string) FriendSeat {
		return FriendSeat{Name: "zhi", Width: 4, Status: Up, Class: "flash,pro", Running: running}
	}

	t.Run("four dealt, one started: three return, and the next deal gives her two", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
		startTick(w, seat())
		require.Equal(t, 4, w.s.Fleet.Count(zhi, Working), "her row is filled to her width")

		// inside the window nothing returns, started or not
		w.tick(FriendStartWindowDefault - time.Minute)
		require.Empty(t, startTick(w, seat("s1-1.w1")).Units)
		require.Equal(t, 4, w.s.Fleet.Count(zhi, Working))

		// past it, the three her beat does not name go back to the pool
		w.tick(2 * time.Minute)
		sp := startTick(w, seat("s1-1.w1"))
		assert.Len(t, sp.Units, 3)
		notes := notStartedNotes(w)
		require.Len(t, notes, 3)
		for _, n := range notes {
			assert.Equal(t, "not started by zhi in 20m; back to the pool", n.What)
			assert.Equal(t, NTakenBack, n.Type)
		}
		assert.Equal(t, 1, friendLoad(w.s, "zhi"), "only the card she started stays")
		assert.Equal(t, zhi, w.s.Fleet.Card("s1-1.w1").Row)
		for _, id := range []string{"s1-2", "s1-3", "s1-4"} {
			wc := w.s.Fleet.Card(id + ".w1")
			require.NotNil(t, wc)
			assert.NotEqual(t, zhi, wc.Row, "%s is placed again off her row", id)
			assert.Equal(t, Working, w.s.StateOf(id), "%s is dealt again from the pool in the same tick", id)
		}
		lanes, ok := w.s.Fleet.Prop(PropFriendLanes("zhi"))
		require.True(t, ok)
		assert.Equal(t, "1", lanes, "her effective width is the cards she started")

		// her started card finishes; the next deal gives her two (DealAhead times one
		// lane), not eight: one into her lane, one ready behind it
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: zhi, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
		var more []CardAdd
		for i := range 5 {
			more = append(more, CardAdd{ID: "s2-" + itoa(i+1), Brief: friendBrief("friend")})
		}
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: more}))
		startTick(w, seat())
		assert.Equal(t, 2, friendLoad(w.s, "zhi"), "the next deal gives her two")
		assert.Equal(t, 1, w.s.Fleet.Count(zhi, Working))
		assert.Equal(t, 1, w.s.Fleet.Count(zhi, Ready))

		// she starts both: her started lanes rise to two, her room to four
		var hers []string
		for _, c := range append(w.s.Fleet.Cell(zhi, Working), w.s.Fleet.Cell(zhi, Ready)...) {
			hers = append(hers, c.ID)
		}
		w.must(Add(w.s, AddReq{Stream: "s3", Cards: []CardAdd{{ID: "s3-1", Brief: friendBrief("friend")}, {ID: "s3-2", Brief: friendBrief("friend")}, {ID: "s3-3", Brief: friendBrief("friend")}}}))
		startTick(w, seat(hers...))
		lanes, _ = w.s.Fleet.Prop(PropFriendLanes("zhi"))
		assert.Equal(t, "2", lanes, "rising again as she starts")
		assert.Equal(t, 4, friendLoad(w.s, "zhi"))
		assert.Equal(t, 2, w.s.Fleet.Count(zhi, Working), "her ready card she started is taken into her second lane")
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("her width is the ceiling: started lanes at her width take the cap off", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
		two := FriendSeat{Name: "zhi", Width: 2, Status: Up, Class: "flash,pro"}
		startTick(w, two)
		w.tick(FriendStartWindowDefault)
		running := two
		running.Running = []string{"s1-1.w1"}
		startTick(w, running)
		lanes, _ := w.s.Fleet.Prop(PropFriendLanes("zhi"))
		require.Equal(t, "1", lanes)
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{{ID: "s2-1", Brief: friendBrief("friend")}}}))
		startTick(w, running)
		require.Equal(t, Ready, w.s.Fleet.Card("s2-1.w1").Col)
		both := two
		both.Running = []string{"s1-1.w1", "s2-1.w1"}
		startTick(w, both)
		lanes, _ = w.s.Fleet.Prop(PropFriendLanes("zhi"))
		assert.Equal(t, "", lanes, "at her width she is not capped")
		read := StartedSeats(w.s, []FriendSeat{both})
		assert.False(t, read[0].Capped)
	})

	t.Run("a hard pin to her stays, and the setting is the window", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("only friend zhi"), friendBrief("friend"))
		w.s.Work.SetProp(PropFriendStartWindow, "5m")
		startTick(w, seat())
		w.tick(5 * time.Minute)
		sp := startTick(w, seat())
		require.Len(t, sp.Units, 1)
		assert.Equal(t, zhi, w.s.Fleet.Card("s1-1.w1").Row, "the pool for a hard pin is her alone")
		require.Len(t, notStartedNotes(w), 1)
		assert.Equal(t, "not started by zhi in 5m; back to the pool", notStartedNotes(w)[0].What)
	})

	t.Run("a reader-first friend is dealt reads before work", func(t *testing.T) {
		t.Parallel()
		deal := func(roles ...string) (*world, *Card) {
			w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
			w.s.Work.Put(&Card{ID: "s1-9", Row: "s1", Col: Review, Score: 9, Rev: 1, Fields: map[string]string{
				"kind": "primary", "attempt": "1", "stream": "s1", "brief": "s1-9: read this (s1) tier: frontier\n\nAS A READ\nread it\n", "head": "h"}})
			putAttemptWork(w, "s1-9", 1, "sprint/s1-9", "h", "")
			f := FriendSeat{Name: "zhi", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RouteFrontier}, Roles: roles}
			startTick(w, f)
			askReaders(t, w, []FriendSeat{f})
			return w, w.s.Fleet.Card(ReadCardID("s1-9", 1, "zhi"))
		}
		w, rc := deal("reader", "builder")
		require.NotNil(t, rc)
		assert.Equal(t, Working, rc.Col, "her read keeps a lane: reads before work")
		assert.Equal(t, 4, friendLoad(w.s, "zhi"), "her room holds the read and three work cards")
		w, rc = deal("builder", "reader")
		assert.Nil(t, rc, "a builder first is dealt work first: her room is full and the read waits")
		assert.Equal(t, 4, w.s.Fleet.Count(FriendRow("zhi"), Working)+w.s.Fleet.Count(FriendRow("zhi"), Ready))
	})
}

package sprint

import (
	"slices"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend is dealt by the lanes she starts, not by her width alone (docs/SPEC-SPRINT.md
// section 1, a friend is dealt what her session starts; tla/FriendStartedLanes.tla). The
// night of 2026-10-05: a friend at width 8 held seven heavy builds for six hours and
// started none, while she did every audit, carry and read she was handed at once.

// addFriendCards adds n cards of the brief to stream s1 after the ones there, ids
// s1-<from>, s1-<from+1>, ...
func addFriendCards(w *world, from, n int, brief string) {
	w.t.Helper()
	var cards []CardAdd
	for i := range n {
		cards = append(cards, CardAdd{ID: "s1-" + itoa(from+i), Brief: brief})
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: cards}))
}

// onRow is the ids of the cards on the friend's row in the column.
func onRow(w *world, friend, col string) []string {
	var out []string
	for _, c := range w.s.Fleet.Cell(FriendRow(friend), col) {
		out = append(out, c.ID)
	}
	return out
}

func TestAFriendIsDealtOnlyWhatHerSessionStarts(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief, brief, brief, brief)
	seat := FriendSeat{Name: "zhi", Width: 4, Status: Up, Class: "flash,pro"}

	// four cards dealt to her, ready until she starts one: her width is not a working count
	dealWith(w, seat)
	require.ElementsMatch(t, []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"}, onRow(w, "zhi", Ready))
	require.Empty(t, onRow(w, "zhi", Working))

	// her beat starts one of them; inside the window the other three stay ready
	seat.Running = []string{"s1-1.w1"}
	w.tick(FriendStartWindowDefault - time.Minute)
	p := dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "her start moves that one to working: %v", p.Units)
	assert.ElementsMatch(t, []string{"s1-2.w1", "s1-3.w1", "s1-4.w1"}, onRow(w, "zhi", Ready))

	// past the window the three she has not started go back to the pool, each with its NOTE line.
	// she is the only friend up and her beat names other work, so the start-bound level will not move them
	w.tick(2 * time.Minute)
	p = dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "the one she started stays with her and finishes")
	assert.Empty(t, onRow(w, "zhi", Ready))
	var notes []string
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == NTakenBack {
				notes = append(notes, n.What)
			}
		}
	}
	require.Len(t, notes, 3)
	for _, n := range notes {
		assert.Equal(t, "not started by zhi in 20m; back to the pool", n)
	}
	for _, id := range []string{"s1-2", "s1-3", "s1-4"} {
		wc := w.s.Fleet.Card(id + ".w1")
		require.NotNil(t, wc)
		assert.Equal(t, Withdrawn, wc.Col, "%s is withdrawn off her row", wc.ID)
		assert.Equal(t, "not started by zhi in 20m; back to the pool", wc.F(FieldTakenBack))
		assert.Equal(t, FriendRow("zhi"), wc.F(FieldTakenFrom), "no deal gives it back to her")
		assert.Equal(t, Ready, w.s.StateOf(id), "its primary is back in the pool")
	}
	assert.Empty(t, Check(w.s, nil), "what is always true holds with her cards returned")

	// the next deal gives her two ready: her started card and DealAhead times her started
	// lanes, never the three she did not start, and none of it working until she starts it
	addFriendCards(w, 5, 4, brief)
	dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working))
	assert.ElementsMatch(t, []string{"s1-5.w1", "s1-6.w1"}, onRow(w, "zhi", Ready))
	for _, id := range []string{"s1-2", "s1-3", "s1-4"} {
		wc := w.s.Fleet.Card(w.s.Primary(id).F("work"))
		if wc != nil {
			assert.NotEqual(t, FriendRow("zhi"), wc.Row, "%s never goes back to her", id)
		}
		assert.NotContains(t, onRow(w, "zhi", Ready), id+".w1")
		assert.NotContains(t, onRow(w, "zhi", Working), id+".w1")
	}
	assert.Equal(t, "zhi:1", w.s.Fleet.Card("s1-5.w1").F(FieldStartedLanes), "her started lanes are recorded on what she is dealt")

	// a tick later, with nothing more started, nothing more is dealt to her
	dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working))
	assert.ElementsMatch(t, []string{"s1-5.w1", "s1-6.w1"}, onRow(w, "zhi", Ready))

	// she starts the next one: her lanes rise to two, that card goes to working, and she is
	// dealt again, ready, never past her width and never past started plus DealAhead times her lanes
	seat.Running = []string{"s1-1.w1", "s1-5.w1"}
	addFriendCards(w, 9, 4, brief)
	dealWith(w, seat)
	assert.ElementsMatch(t, []string{"s1-1.w1", "s1-5.w1"}, onRow(w, "zhi", Working), "working is what she has started")
	assert.LessOrEqual(t, len(onRow(w, "zhi", Working)), seat.Width, "her width stays the ceiling")
	held := len(onRow(w, "zhi", Working)) + len(onRow(w, "zhi", Ready))
	assert.LessOrEqual(t, held, 2+DealAhead*2, "her room is her started cards and DealAhead times her lanes")
	assert.NotEmpty(t, onRow(w, "zhi", Ready), "what she has not started stays ready")
	st := friendStartedLanes(w.s, seat)
	assert.Equal(t, 2, st.lanes)
	assert.True(t, st.throttled)
	assert.Empty(t, Check(w.s, nil))
}

// A friend who starts none of her cards has them all returned past the window (the night of
// 2026-10-05: a friend at width 8 held seven heavy builds and started none), and is then dealt
// by her started lanes' floor (at least 1), never her width again.
func TestAFriendWhoStartsNoneHasHerCardsReturnedAndIsDealtByHerLanes(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief, brief, brief, brief)
	seat := FriendSeat{Name: "zhi", Width: 4, Status: Up, Class: "flash,pro"}

	dealWith(w, seat)
	require.ElementsMatch(t, []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"}, onRow(w, "zhi", Ready))

	// she is the only friend up and her beat names nothing running: past the window all four
	// go back to the pool, each with its NOTE line
	w.tick(FriendStartWindowDefault + time.Minute)
	dealWith(w, seat)
	assert.Empty(t, onRow(w, "zhi", Ready))
	assert.Empty(t, onRow(w, "zhi", Working))
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		wc := w.s.Fleet.Card(id + ".w1")
		require.NotNil(t, wc)
		assert.Equal(t, Withdrawn, wc.Col, "%s is withdrawn off her row", wc.ID)
		assert.Equal(t, "not started by zhi in 20m; back to the pool", wc.F(FieldTakenBack))
		assert.Equal(t, FriendRow("zhi"), wc.F(FieldTakenFrom))
		assert.Equal(t, Ready, w.s.StateOf(id), "its primary is back in the pool")
	}

	// her started lanes are at least 1, and the next deal gives her DealAhead times them, never
	// her width: two ready, none working until she starts one
	addFriendCards(w, 5, 4, brief)
	dealWith(w, seat)
	assert.Empty(t, onRow(w, "zhi", Working))
	assert.ElementsMatch(t, []string{"s1-5.w1", "s1-6.w1"}, onRow(w, "zhi", Ready))
	st := friendStartedLanes(w.s, seat)
	assert.Equal(t, 1, st.lanes)
	assert.True(t, st.throttled)
	assert.Empty(t, Check(w.s, nil))
}

// The start window is the sprint's setting, and the card she started by a progress stamp
// is started too.
func TestTheStartWindowIsASettingAndAProgressStampIsAStart(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief, brief)
	w.s.Work.SetProp(PropFriendStartWindow, "1h")
	require.Equal(t, time.Hour, w.s.FriendStartWindow())
	seat := FriendSeat{Name: "zhi", Width: 2, Status: Up, Class: "flash,pro"}
	dealWith(w, seat)
	require.ElementsMatch(t, []string{"s1-1.w1", "s1-2.w1"}, onRow(w, "zhi", Ready))
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)

	w.tick(30 * time.Minute)
	dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "a progress stamp is her start")
	assert.Equal(t, []string{"s1-2.w1"}, onRow(w, "zhi", Ready), "inside the hour the other stays")

	w.tick(31 * time.Minute)
	p := dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "the stamped card is started")
	assert.Empty(t, onRow(w, "zhi", Ready))
	var what string
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == NTakenBack {
				what = n.What
			}
		}
	}
	assert.Contains(t, what, "not started by zhi in 1h; back to the pool")

	// at her width her lanes are her width again, and the record says so
	seat.Width = 1
	st := friendStartedLanes(w.s, seat)
	assert.False(t, st.throttled, "started lanes at her width: dealt as before")
}

// A one-shot friend's lane is one card at a time already: the start window does not apply.
func TestAOneShotFriendsCardIsNotReturnedByTheStartWindow(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief)
	seat := FriendSeat{Name: "zhi", Width: 2, Status: Up, Class: "flash,pro", Mode: "one-shot"}
	dealWith(w, seat)
	require.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Ready), "one-shot deals one card, ready until she starts it")
	w.tick(2 * FriendStartWindowDefault)
	dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Ready), "the start window does not take a one-shot card back")
	assert.Empty(t, onRow(w, "zhi", Withdrawn))
}

// A friend whose roles are reader-first (reader, not builder) is dealt reads before work:
// the deal keeps her room for the reads that wait for her, and the tick's ask fills it.
func TestAReaderFirstFriendIsDealtReadsBeforeWork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		roles []string
		work  int
	}{{[]string{"reader"}, 0}, {[]string{"builder", "reader"}, 2}} {
		w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
		putReview(w, "s1-8", "s1-8: work (s1) tier: frontier\n", 1, 8, "abc")
		putReview(w, "s1-9", "s1-9: work (s1) tier: frontier\n", 1, 9, "abc")
		seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RouteFrontier}, Roles: tc.roles}
		require.Equal(t, 2, readsWaitingFor(w.s, seat))
		require.True(t, readerFirst(seat) == !slices.Contains(tc.roles, "builder"))
		// friendDeal, not the tick: the tick's ladder asks every friend's reads first, and
		// this reservation is the reader-first friend's own
		p, _, _ := friendDeal(w.s, w.s.Work.Column(Ready), []FriendSeat{seat})
		w.must(p)
		n := 0
		for _, c := range append(w.s.Fleet.Cell(FriendRow("amy"), Working), w.s.Fleet.Cell(FriendRow("amy"), Ready)...) {
			if c.F("kind") == "work" {
				n++
			}
		}
		assert.Equal(t, tc.work, n, "roles %v: work dealt before the reads", tc.roles)
		askReaders(t, w, []FriendSeat{seat})
		if tc.work > 0 {
			assert.Nil(t, w.s.Fleet.Card(ReadCardID("s1-8", 1, "amy")), "roles %v: work filled her room first", tc.roles)
			continue
		}
		require.NotNil(t, w.s.Fleet.Card(ReadCardID("s1-8", 1, "amy")), "roles %v: her first read is asked", tc.roles)
		assert.Equal(t, Working, w.s.Fleet.Card(ReadCardID("s1-8", 1, "amy")).Col, "a reader-first friend's lane is a read's")
		assert.NotNil(t, w.s.Fleet.Card(ReadCardID("s1-9", 1, "amy")), "and the room behind it too")
	}
}

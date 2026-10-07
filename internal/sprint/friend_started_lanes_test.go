package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend is dealt by the lanes she starts, not by her width alone (docs/SPEC-SPRINT.md
// section 1, a friend is dealt what her session starts). The night of 2026-10-05: a friend
// at width 8 held seven heavy builds for six hours and started none, while she did every
// audit, carry and read she was handed at once.

// addFriendCards adds n cards of the brief to stream s1, ids s1-<from> onward.
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

// notStartedNotes is the NOTE lines of cards the deal sent back to the pool.
func notStartedNotes(p Plan) []string {
	var notes []string
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == NTakenBack {
				notes = append(notes, n.What)
			}
		}
	}
	return notes
}

func TestAFriendIsDealtOnlyWhatHerSessionStarts(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief, brief, brief, brief)
	seat := FriendSeat{Name: "zhi", Width: 4, Status: Up, Class: "flash,pro"}

	// four cards dealt to her, ready until she starts one
	dealWith(w, seat)
	require.ElementsMatch(t, []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"}, onRow(w, "zhi", Ready))
	require.Empty(t, onRow(w, "zhi", Working))

	// her beat starts one of them; inside the window nothing goes back
	seat.Running = []string{"s1-1.w1"}
	w.tick(FriendStartMaxDefault - time.Minute)
	p := dealWith(w, seat)
	require.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "her beat moves the one she started into working")
	require.ElementsMatch(t, []string{"s1-2.w1", "s1-3.w1", "s1-4.w1"}, onRow(w, "zhi", Ready), "inside the start window her cards stay: %v", p.Units)
	require.Empty(t, notStartedNotes(p))

	// past the window the three she has not started go back to the pool, each with its NOTE line
	w.tick(2 * time.Minute)
	p = dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "the one she started stays with her")
	assert.Empty(t, onRow(w, "zhi", Ready))
	notes := notStartedNotes(p)
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

	// the next deal gives her two, and never the three she did not start. The fleet may
	// take a returned primary; it does not go back onto her row.
	addFriendCards(w, 5, 4, brief)
	dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working))
	ready := onRow(w, "zhi", Ready)
	assert.Len(t, ready, 2, "the next deal gives her two")
	for _, id := range []string{"s1-2.w1", "s1-3.w1", "s1-4.w1"} {
		assert.NotContains(t, ready, id, "%s is not dealt to her again", id)
		wc := w.s.Fleet.Card(id)
		require.NotNil(t, wc)
		onHer := wc.Row == FriendRow("zhi") && (wc.Col == Ready || wc.Col == Working)
		assert.False(t, onHer, "%s is not back on her row (%s %s)", id, wc.Row, wc.Col)
	}
	recorded := false
	for _, id := range onRow(w, "zhi", Ready) {
		if w.s.Fleet.Card(id).F("started_lanes") == "zhi:1" {
			recorded = true
		}
	}
	assert.True(t, recorded, "her started lanes are recorded on what she is dealt")

	// a tick later, with nothing more started, nothing more is dealt to her
	dealWith(w, seat)
	assert.Len(t, onRow(w, "zhi", Working), 1)
	assert.Len(t, onRow(w, "zhi", Ready), 2)

	// she starts the next one: her lanes rise, and she is dealt again, never past her width
	var next string
	for _, id := range onRow(w, "zhi", Ready) {
		next = id
		break
	}
	seat.Running = []string{"s1-1.w1", next}
	addFriendCards(w, 9, 4, brief)
	dealWith(w, seat)
	held := len(onRow(w, "zhi", Working)) + len(onRow(w, "zhi", Ready))
	assert.Greater(t, held, 3, "her lanes rose, so the deal fills the room they open")
	assert.LessOrEqual(t, held, DealAhead*seat.Width, "her width stays the ceiling")
	assert.Empty(t, Check(w.s, nil))
}

// The start window is the sprint's setting, and a progress stamp is a start too.
func TestTheStartWindowIsASettingAndAProgressStampIsAStart(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief, brief)
	w.s.Work.SetProp(PropFriendStartMax, "1h")
	require.Equal(t, time.Hour, w.s.FriendStartMax())
	seat := FriendSeat{Name: "zhi", Width: 2, Status: Up, Class: "flash,pro", Running: []string{"s1-1.w1"}}
	dealWith(w, seat)
	require.Len(t, append(onRow(w, "zhi", Ready), onRow(w, "zhi", Working)...), 2)
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)

	w.tick(30 * time.Minute)
	dealWith(w, seat)
	assert.Len(t, append(onRow(w, "zhi", Ready), onRow(w, "zhi", Working)...), 2, "inside the hour her cards stay")

	w.tick(31 * time.Minute)
	p := dealWith(w, seat)
	assert.Equal(t, []string{"s1-1.w1"}, onRow(w, "zhi", Working), "the stamped card is started")
	require.NotEmpty(t, p.Units)
	assert.Contains(t, notStartedNotes(p), "not started by zhi in 1h; back to the pool")
}

// A one-shot friend's lane is one card at a time already: the start window does not apply.
func TestAOneShotFriendsCardIsNotReturnedByTheStartWindow(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend zhi")
	w := friendWorld(t, brief)
	seat := FriendSeat{Name: "zhi", Width: 2, Status: Up, Class: "flash,pro", Mode: "one-shot"}
	dealWith(w, seat)
	require.Len(t, onRow(w, "zhi", Ready), 1)
	w.tick(2 * FriendStartMaxDefault)
	seat.Running = []string{"s1-1.w1"}
	dealWith(w, seat)
	assert.Empty(t, onRow(w, "zhi", Withdrawn))
	assert.Len(t, append(onRow(w, "zhi", Ready), onRow(w, "zhi", Working)...), 1)
}

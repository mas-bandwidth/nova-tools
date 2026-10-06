package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rework keeps the WHO pin (the owner, 2026-10-05: a rework of a friend's own rating,
// WHO: friend <name>, was dealt to a bud, and another to a second friend): a card whose
// WHO line names a friend, come back by a rework, a return or a redo, is dealt only to
// her, as on its first deal; while she is down or held it waits ready, dealt to no other
// friend and no machine (ReworkPinned), and the tick deals it to her once she is up.
func TestAReworkKeepsTheWhoPin(t *testing.T) {
	t.Parallel()
	amy := func(status string) FriendSeat {
		return FriendSeat{Name: "amy", Width: 2, Status: status, Class: "flash,pro"}
	}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}

	// waitsForAmy deals with amy down, then held, bob up with room and the machines up:
	// the card stays ready, and no attempt of it reaches bob or a machine; then amy up,
	// and its next attempt is hers.
	waitsForAmy := func(t *testing.T, w *world, id string) {
		t.Helper()
		require.Equal(t, Ready, w.s.StateOf(id))
		require.Equal(t, FriendRow("amy"), w.s.Primary(id).F(FieldWho), "the WHO line survives on the row")
		next := WorkCardID(id, w.s.Primary(id).Int("attempt")+1)
		for _, st := range []string{Down, Held} {
			dealWith(w, amy(st), bob)
			assert.Equal(t, Ready, w.s.StateOf(id), "it waits for amy while she is %s", st)
			assert.Nil(t, w.s.Fleet.Card(next), "no next attempt while amy is %s", st)
			assert.Empty(t, w.s.Fleet.Cell(FriendRow("bob"), Ready), "bob is dealt nothing")
			assert.Empty(t, w.s.Fleet.Cell(FriendRow("bob"), Working), "bob is dealt nothing")
			for _, m := range w.s.Members() {
				for _, c := range append(w.s.Fleet.Cell(m, Ready), w.s.Fleet.Cell(m, Working)...) {
					assert.NotEqual(t, id, c.F("primary"), "machine %s is dealt nothing of %s", m, id)
				}
			}
		}
		// the machines' deal verb refuses it as a friend's card
		p := Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "a friend's card")

		dealWith(w, amy(Up), bob)
		wc := w.s.Fleet.Card(next)
		require.NotNil(t, wc, "amy up: its next attempt is dealt")
		assert.Equal(t, FriendRow("amy"), wc.Row, "and it is hers")
	}

	t.Run("rework", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend amy"))
		// dealt ready on her row, then started by her (her start receipt): working, so her
		// finish takes it (docs/SPEC-SPRINT.md, "Working on her row means started")
		dealStarted(w, amy(Up), bob)
		require.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row, "its first deal is to the friend it names")
		require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "she started it")
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "HOLD: the gate is red"}))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
		waitsForAmy(t, w, "s1-1")
		assert.Equal(t, "make the gate green", w.s.Fleet.Card("s1-1.w2").F("fix"))
	})
	t.Run("return then rework", func(t *testing.T) {
		t.Parallel()
		w := stoppedForConflict(t)
		w.s.Work.Card("s1-2").Fields[FieldWho] = FriendRow("amy")
		w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "suspect"}))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "a fix"}))
		waitsForAmy(t, w, "s1-2")
	})
	t.Run("redo", func(t *testing.T) {
		t.Parallel()
		w := stoppedForConflict(t)
		w.s.Work.Card("s1-2").Fields[FieldWho] = FriendRow("amy")
		w.must(Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-2"}}, Who: "coordinator"}))
		waitsForAmy(t, w, "s1-2")
	})
	t.Run("a first deal is a preference", func(t *testing.T) {
		t.Parallel()
		// unchanged: a WHO: friend <name> card never dealt goes to a friend up when she is down
		w := friendWorld(t, friendBrief("friend amy"))
		dealWith(w, amy(Down), bob)
		require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
		assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row)
	})
}

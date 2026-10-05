package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rework keeps the WHO pin (the owner, 2026-10-05: a rework of a friend's own rating,
// WHO: friend <name>, was dealt to a machine, and another to a second friend): a card
// whose WHO line names a friend, come back by a rework, a return or a redo, is dealt only
// to her, as on its first deal; while she is down or held it waits ready, dealt to no
// other friend and no machine (ReworkPinned).
func TestAReworkKeepsTheWhoPin(t *testing.T) {
	t.Parallel()
	amy := func(status string) FriendSeat {
		return FriendSeat{Name: "amy", Width: 2, Status: status, Class: "flash,pro"}
	}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, amy(Up), bob)
	require.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row, "its first deal is to the friend it names")

	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy HOLD: the gate is red"}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho), "the rework keeps the WHO line")
	assert.True(t, ReworkPinned(w.s.Primary("s1-1")))

	// she is down, then held: neither another friend up with room nor a machine is dealt it
	for _, st := range []string{Down, Held} {
		dealWith(w, amy(st), bob)
		assert.Equal(t, Ready, w.s.StateOf("s1-1"), "it waits for amy while she is %s", st)
		assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "no attempt 2 while amy is %s", st)
		assert.Zero(t, w.s.Fleet.Count(FriendRow("bob"), Working)+w.s.Fleet.Count(FriendRow("bob"), Ready), "bob is dealt nothing")
		for _, m := range []string{"m1", "m2"} {
			assert.Zero(t, w.s.Fleet.Count(m, Ready)+w.s.Fleet.Count(m, Working), "machine %s is dealt nothing", m)
		}
	}
	// the machines' deal verb refuses it by name
	p := Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a friend's card")
	mustHold(t, running(w), "s1-1", HeldByWaiting) // waits for only friend amy

	// she is up: attempt 2 is hers, with its fix
	dealWith(w, amy(Up), bob)
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, "make the gate green", wc.F("fix"))

	// a first deal is still a preference: amy down, the card goes to bob
	w2 := friendWorld(t, friendBrief("friend amy"))
	assert.False(t, ReworkPinned(w2.s.Primary("s1-1")))
	dealWith(w2, amy(Down), bob)
	require.NotNil(t, w2.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, FriendRow("bob"), w2.s.Fleet.Card("s1-1.w1").Row, "WHO: friend <name> is a preference on its first deal")
}

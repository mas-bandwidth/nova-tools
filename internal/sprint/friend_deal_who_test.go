package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dealer honors WHO (2026-10-06: a card whose brief named one friend was dealt to
// another friend's row twice while she was up with queue 0 and width 8, and the
// coordinator could not move it; docs/SPEC-SPRINT.md section 1, a friend's card;
// tla/Deal.tla, WhoIsHonored). A WHO line that names a friend is a true-ownership pin: the
// card is dealt to her row alone, waits ready for her while she is not up with room, and
// friend take <friend> <id> moves a ready or dealt-but-not-taken card onto her row.

func TestACardWithWhoIsDealtOnlyToThatFriend(t *testing.T) {
	t.Parallel()
	fay := func(status string) FriendSeat {
		return FriendSeat{Name: "fay", Width: 8, Status: status, Class: "flash,pro"}
	}
	other := FriendSeat{Name: "gus", Width: 8, Status: Up, Class: "flash,pro"}

	// she is up with room: dealt to her row at the tick, however much room another has
	w := friendWorld(t, friendBrief("friend fay"))
	require.Equal(t, FriendRow("fay"), w.s.Primary("s1-1").F(FieldWho), "the WHO line is stored on the card")
	dealWith(w, other, fay(Up))
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("fay"), wc.Row)
	assert.Equal(t, Working, wc.Col)
	assert.Empty(t, Check(w.s, nil))

	// she is down, or held: it waits ready, held for her by name; no other friend and
	// no machine is dealt it, tick after tick
	for _, status := range []string{Down, Held} {
		w := friendWorld(t, friendBrief("friend fay"))
		for range 3 {
			dealWith(w, other, fay(status))
		}
		assert.Nil(t, w.s.Fleet.Card("s1-1.w1"), "%s: never dealt elsewhere", status)
		assert.Equal(t, Ready, w.s.StateOf("s1-1"))
		assert.Empty(t, w.s.Fleet.Cell(FriendRow("gus"), Ready))
		assert.Empty(t, w.s.Fleet.Cell(FriendRow("gus"), Working))
		hd := mustHold(t, running(w), "s1-1", HeldByWaiting)
		assert.Contains(t, hd.Why, "waits for friend fay")

		// the machines' deal verb refuses it by name too
		p := Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "a friend's card")

		// she comes up: the next tick deals it to her
		dealWith(w, other, fay(Up))
		wc := w.s.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc)
		assert.Equal(t, FriendRow("fay"), wc.Row)
	}

	// she is up with no room: it waits, never to the other friend with room
	full := FriendSeat{Name: "fay", Width: 0, Status: Up, Class: "flash,pro"}
	w = friendWorld(t, friendBrief("friend fay"))
	dealWith(w, other, full)
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
}

func TestFriendTakeMovesAReadyCardOntoTheNamedFriendsRow(t *testing.T) {
	t.Parallel()
	// a ready card, never dealt (here: its friend is down, so it waits)
	w := friendWorld(t, friendBrief("friend fay"))
	dealWith(w, FriendSeat{Name: "fay", Width: 2, Status: Down})
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "fay", IDs: []string{"s1-1"}, Reason: "Glenn asked her by name", Who: "rowan"}))
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("fay"), wc.Row)
	assert.Equal(t, Ready, wc.Col, "placed ready: her lane takes it at the tick")
	assert.Equal(t, Working, w.s.StateOf("s1-1"))
	assert.Equal(t, "s1-1.w1", w.s.Primary("s1-1").F("work"))
	assert.Empty(t, Check(w.s, nil))
	dealWith(w, FriendSeat{Name: "fay", Width: 2, Status: Up, Class: "flash"})
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "the deal takes her ready card into her free lane")

	// a card dealt to a machine and not taken there: moved onto her row at its next generation
	w = friendWorld(t, friendBrief("friend"), "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	mc := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, mc)
	require.Equal(t, Ready, mc.Col)
	from := mc.Row
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "fay", IDs: []string{"s1-2.w1"}}))
	mc = w.s.Fleet.Card("s1-2.w1")
	assert.Equal(t, FriendRow("fay"), mc.Row)
	assert.Equal(t, Ready, mc.Col)
	assert.Equal(t, "2", mc.F("gen"), "its own branch and job")
	assert.Equal(t, FriendRow("fay"), mc.F("member"))
	assert.Empty(t, w.s.Fleet.Cell(from, Ready))
	assert.Empty(t, Check(w.s, nil))

	// a card whose WHO line names another friend is refused: never on another friend's row
	w = friendWorld(t, friendBrief("friend fay"))
	p0 := FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1"}})
	require.Len(t, p0.Refused, 1)
	assert.Contains(t, p0.Refused[0].Why, "its WHO line names friend fay, not amy")
	assert.Empty(t, p0.Units)

	// a taken card is refused, naming the lane it works in
	w = friendWorld(t, friendBrief("friend"))
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"})
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	p := FriendTake(w.s, FriendTakeReq{Friend: "fay", IDs: []string{"s1-1"}})
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "s1-1.w1 is taken: it works in the lane at friend.amy:working; it stays there and finishes", p.Refused[0].Why)
	assert.Empty(t, p.Units)
}

package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One live lane per card (docs/SPEC-SPRINT.md section 1, one lane per card): the friends'
// deal never places a card on a second row while a friend's beat names it running. The
// night of 2026-10-05 a card handed back off one friend was dealt to another while the
// first friend's lane still ran it.
func TestTheDealPlacesNoCardOnASecondRowWhileALaneRunsIt(t *testing.T) {
	t.Parallel()
	amy, bob := FriendRow("amy"), FriendRow("bob")
	bobUp := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash"}
	// handedBack is s1-1 dealt to amy, then amy held with her cards handed back: its work
	// card withdrawn off her row, its primary ready
	handedBack := func(t *testing.T) *world {
		w := friendWorld(t, friendBrief("friend"))
		dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"}, FriendSeat{Name: "bob", Width: 2, Status: Down, Class: "flash"})
		require.Equal(t, amy, w.s.Fleet.Card("s1-1.w1").Row)
		w.must(HoldNames(w.s, HoldReq{Names: []string{"amy"}, Return: true, Reason: "held", Who: "coordinator", Friends: []string{"amy", "bob"}}))
		require.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
		require.Equal(t, Ready, w.s.StateOf("s1-1"))
		return w
	}
	for _, running := range []string{"s1-1.w1", "s1-1", "s1-1.w1.g2"} {
		t.Run("her beat names "+running+" running: no second row", func(t *testing.T) {
			t.Parallel()
			w := handedBack(t)
			amyRunning := FriendSeat{Name: "amy", Width: 2, Status: Held, Class: "flash", Running: []string{running}}
			p := FriendDeal(w.s, []*Card{w.s.Primary("s1-1")}, []FriendSeat{amyRunning, bobUp})
			assert.Empty(t, p.Units, "her lane still runs it: it waits ready")
			assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
		})
	}
	t.Run("once her beat stops naming it, it is dealt", func(t *testing.T) {
		t.Parallel()
		w := handedBack(t)
		w.must(FriendDeal(w.s, []*Card{w.s.Primary("s1-1")}, []FriendSeat{{Name: "amy", Width: 2, Status: Held, Class: "flash"}, bobUp}))
		assert.Equal(t, bob, w.s.Fleet.Card("s1-1.w1").Row)
		assert.Equal(t, Working, w.s.StateOf("s1-1"))
		assert.Empty(t, Check(w.s, nil))
	})
}

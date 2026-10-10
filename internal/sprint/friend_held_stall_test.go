package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A card whose tier friends alone serve (the fleet's pro routes off, a friend up with pro
// on her row: tierServed) is the friends' deal's, never a machine's, so the machines' room
// holds nothing for it. On 2026-10-06 the no-stall rule counted only the machines' room and
// called every such card waiting for stella's room "stalled".

// friendTierWorld is n primaries of s1 pinned to tier pro and back in ready, two machines up
// with room, a flash route enabled and the pro route disabled, stella up with the width
// given and pro on her row.
func friendTierWorld(t *testing.T, n, width int) (*world, FriendSeat) {
	t.Helper()
	w := setup(t, n)
	stella := FriendSeat{Name: "stella", Width: width, Status: Up, Class: "heavy,pro"}
	var ids []string
	for i := 1; i <= n; i++ {
		id := "s1-" + itoa(i)
		finished(w, id, true)
		ids = append(ids, id)
	}
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
		{Name: "pro-off", Tier: cardhdr.RoutePro, Provider: "p", Model: "pro", Enabled: false},
	}
	w.s.Friends = []FriendSeat{stella}
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: ids}, Fix: "make it green", Tier: cardhdr.RoutePro}))
	for _, id := range ids {
		require.Equal(t, Ready, w.state(id))
	}
	return w, stella
}

func TestAFriendHeldCardIsNotAStall(t *testing.T) {
	t.Parallel()
	t.Run("stella at her room, machines with room: it waits for her room", func(t *testing.T) {
		t.Parallel()
		w, stella := friendTierWorld(t, 3, 1)
		dealWith(w, stella)
		require.Equal(t, 2, friendLoad(w.s, "stella"), "her room, DealAhead times her width, is full")
		require.Equal(t, Ready, w.state("s1-3"))
		require.Positive(t, widthRoom(w.s, w.s.UpMembers()), "the machines have room")
		hd := mustHold(t, running(w), "s1-3", HeldByWaiting)
		assert.Contains(t, hd.Why, "waits for a friend serving tier pro below her room")
	})
	t.Run("stella with room: the next tick deals it; a deal that does not is a stall", func(t *testing.T) {
		t.Parallel()
		w, stella := friendTierWorld(t, 3, 1)
		dealWith(w, stella)
		stella.Width = 2 // her room grows to 4: one free place, and s1-3 still ready
		w.s.Friends = []FriendSeat{stella}
		mustHold(t, running(w), "s1-3", HeldByTick)
		// the deal that did not hand it over: the next tick does nothing to it
		c := newHeld(running(w), w.s.Now)
		delete(c.tick, "s1-3")
		hd := c.hold("s1-3")
		require.True(t, hd.Stalled(), "%s", hd)
		assert.Contains(t, hd.Why, "a friend serving its tier is below her room")
	})
}

func TestACardWithdrawnFromItsLastFriendRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	w, stella := friendTierWorld(t, 1, 1)
	dealWith(w, stella)
	require.Equal(t, FriendRow("stella"), w.s.Fleet.Card("s1-1.w2").Row)
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "stella", IDs: []string{"s1-1.w2"}, Reason: "wrong friend"}))
	require.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w2").Col)
	require.Equal(t, Ready, w.state("s1-1"))
	for range 2 {
		dealWith(w, stella)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w2").Col, "never back to the friend it was taken from, nor to a machine")
		open := openOf(w, NNoRoute, StreamSubject(TierSubject(cardhdr.RoutePro)))
		require.Len(t, open, 1, "one judgment of its tier: %v", w.s.Open)
		assert.Contains(t, open[0].Note.Primaries, "s1-1")
		assert.Contains(t, open[0].Note.What, "taken back")
	}
	mustHold(t, running(w), "s1-1", HeldByJudgment)
}

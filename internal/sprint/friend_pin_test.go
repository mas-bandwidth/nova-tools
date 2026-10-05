package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card pinned to a friend who is not up (docs/SPEC-SPRINT.md, "A card pinned to a
// friend who is not up"; friend_pin.go): a card for any friend goes to the next friend up,
// a card naming a down or held friend waits, and FriendPinWaitAfter on it raises ONE
// judgment per friend naming her cards, never again while the wait lasts; her coming up
// clears it, and a later wait counts afresh.

// pinJudgments is the pin judgments of a plan, by the friend each names.
func pinJudgments(p Plan) map[string]Note {
	out := map[string]Note{}
	for _, n := range p.Notes {
		if n.Kind != Judgment || n.Type != NFriendPinned {
			continue
		}
		for _, f := range []string{"amy", "bob", "cy"} {
			if strings.Contains(n.What, "friend "+f+" ") {
				out[f] = n
			}
		}
	}
	return out
}

func TestACardPinnedToADownFriendFallsToTheNextExecutor(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend bob"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Down}, {Name: "bob", Width: 2, Status: Held}, {Name: "cy", Width: 2, Status: Up}}
	tick := func() Plan {
		dealWith(w, seats...)
		return w.part(TickFriendStall, TickReq{Friends: seats})
	}
	t0 := w.s.Now

	// the card for any friend falls to the next friend up; the named ones wait, no machine dealt
	p := tick()
	wc := w.s.Fleet.Card("s1-4.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("cy"), wc.Row)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		assert.Equal(t, Ready, w.s.StateOf(id))
		assert.Nil(t, w.s.Fleet.Card(id+".w1"))
	}
	assert.Empty(t, pinJudgments(p), "no judgment before the bound")
	since, _ := w.s.Fleet.Prop(PropFriendPinSince("amy"))
	assert.Equal(t, stamp(t0), since)
	since, _ = w.s.Fleet.Prop(PropFriendPinSince("cy"))
	assert.Empty(t, since, "nothing waits for a friend up")

	w.s.Now = t0.Add(FriendPinWaitAfter - time.Minute)
	assert.Empty(t, pinJudgments(tick()), "no judgment before the bound")

	// at the bound: one judgment per friend, naming her cards
	w.s.Now = t0.Add(FriendPinWaitAfter + time.Minute)
	js := pinJudgments(tick())
	require.Len(t, js, 2)
	assert.Equal(t, []string{"s1-1", "s1-2"}, js["amy"].Primaries)
	assert.Contains(t, js["amy"].What, "while she is down")
	assert.Equal(t, []string{"s1-3"}, js["bob"].Primaries)
	assert.Contains(t, js["bob"].What, "while she is held")
	assert.Equal(t, w.s.Coordinator, js["amy"].To)

	// one judgment: later ticks do not raise it again
	w.s.Now = t0.Add(2 * FriendPinWaitAfter)
	assert.Empty(t, pinJudgments(tick()))

	// amy up: her cards are dealt to her and her wait clears; bob's stands
	seats[0].Status = Up
	w.s.Now = t0.Add(2*FriendPinWaitAfter + time.Minute)
	assert.Empty(t, pinJudgments(tick()))
	for _, id := range []string{"s1-1", "s1-2"} {
		require.NotNil(t, w.s.Fleet.Card(id+".w1"))
		assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card(id+".w1").Row)
	}
	for _, prop := range []string{PropFriendPinSince("amy"), PropFriendPinJudged("amy")} {
		v, _ := w.s.Fleet.Prop(prop)
		assert.Empty(t, v, prop)
	}
	v, _ := w.s.Fleet.Prop(PropFriendPinJudged("bob"))
	assert.NotEmpty(t, v)

	// bob up and down again: bob's next wait counts afresh and is told once more
	seats[1].Status = Up
	seats[1].Width = 0
	tick()
	v, _ = w.s.Fleet.Prop(PropFriendPinJudged("bob"))
	assert.Empty(t, v, "up clears the wait even with no room")
	seats[1].Status = Down
	t1 := w.s.Now
	tick()
	w.s.Now = t1.Add(FriendPinWaitAfter)
	js = pinJudgments(tick())
	require.Len(t, js, 1)
	assert.Equal(t, []string{"s1-3"}, js["bob"].Primaries)
}

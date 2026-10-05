package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friend take of several cards (docs/SPEC-SPRINT.md section 1, the card
// friend-take-partial.w1): it takes every card named that she may give up and refuses,
// one line each, every card she keeps; --all-or-nothing (AllOrNothing) takes none when
// one is refused.

// partialWorld is amy at width 2 with s1-1 and s1-2 working and s1-3 ready on her row,
// s1-2 started (her beat names it running).
func partialWorld(t *testing.T) (*world, map[string]string) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"})
	startCards(w, "amy", "s1-1.w1", "s1-2.w1")
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	require.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
	require.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
	return w, map[string]string{"s1-2.w1": "her beat names it running"}
}

func TestFriendTakeTakesTheTakeableAndNamesTheRest(t *testing.T) {
	t.Parallel()
	ids := []string{"s1-1", "s1-2", "s1-3"}

	t.Run("takes the takeable", func(t *testing.T) {
		t.Parallel()
		w, started := partialWorld(t)
		p := w.do(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: ids, Started: started, Reason: "rebalance"}))
		require.Len(t, p.Refused, 1, "one REFUSED line, for the one she keeps")
		assert.Equal(t, "s1-2", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "has started: her beat names it running")
		assert.Len(t, p.Units, 2)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-3.w1").Col)
		assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col, "the started one stays with her")
		assert.Equal(t, Ready, w.s.StateOf("s1-1"))
		assert.Equal(t, Ready, w.s.StateOf("s1-3"))
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("all or nothing takes none", func(t *testing.T) {
		t.Parallel()
		w, started := partialWorld(t)
		p := FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: ids, Started: started, AllOrNothing: true})
		assert.Empty(t, p.Units)
		require.Len(t, p.Refused, 3, "the started one, and each that would have been taken")
		assert.Equal(t, "s1-2", p.Refused[0].Key)
		assert.Equal(t, "s1-1.w1", p.Refused[1].Key)
		assert.Equal(t, "not taken: --all-or-nothing, and 1 of the cards named was refused", p.Refused[1].Why)
		assert.Equal(t, "s1-3.w1", p.Refused[2].Key)
		w.do(p)
		assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
	})

	t.Run("all or nothing with none refused takes them all", func(t *testing.T) {
		t.Parallel()
		w, _ := partialWorld(t)
		p := w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1", "s1-3"}, AllOrNothing: true}))
		assert.Len(t, p.Units, 2)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-3.w1").Col)
	})
}

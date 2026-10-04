package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card taken back (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-04, on
// cards dealt to a friend who would not start them: "sounds bad, we should fix this"):
// friend take withdraws the cards named that she has not started, friend down every one,
// and the friends' deal places the same card again, never on the friend it was taken from.

// takeWorld is amy at width 1 with s1-1 working and s1-2 ready behind it on her row, both
// cards for any friend, and bob down.
func takeWorld(t *testing.T) *world {
	t.Helper()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up}, FriendSeat{Name: "bob", Width: 1, Status: Down})
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	require.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col)
	return w
}

func TestTheCoordinatorTakesBackOneOfAFriendsCards(t *testing.T) {
	t.Parallel()
	w := takeWorld(t)
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1"}, Reason: "she is on another job", Who: "rowan"}))
	wc := w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, Withdrawn, wc.Col)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, "taken back by the coordinator: she is on another job", wc.F(FieldTakenBack))
	assert.Equal(t, FriendRow("amy"), wc.F(FieldTakenFrom))
	assert.Empty(t, wc.F(FieldTakeEnded), "no failure: no redeal of its bound is spent")
	assert.Equal(t, "2", wc.F("gen"))
	assert.Equal(t, Ready, w.s.StateOf("s1-1"), "its primary is ready for the next deal")
	assert.Equal(t, "1", w.s.Primary("s1-1").F("attempt"), "a take-back is no attempt")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col, "her lane freed: her ready card is taken into working")
	assert.Empty(t, Check(w.s, nil))
}

func TestATakeOfACardSheHasStartedIsRefused(t *testing.T) {
	t.Parallel()
	w := takeWorld(t)
	p := FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1.w1"}, Started: map[string]string{"s1-1.w1": "a push on its branch"}})
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "s1-1.w1", p.Refused[0].Key)
	assert.Contains(t, p.Refused[0].Why, "has started: a push on its branch")
	assert.Empty(t, p.Units)

	// finished: in review, no longer dealt
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	p = FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1"}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "has started: friend amy finished it")
}

func TestATakeOfACardDealtToAnotherFriendIsRefused(t *testing.T) {
	t.Parallel()
	w := takeWorld(t)
	p := FriendTake(w.s, FriendTakeReq{Friend: "bob", IDs: []string{"s1-1", "nope"}})
	require.Len(t, p.Refused, 2)
	assert.Equal(t, "s1-1.w1 is not dealt to friend bob: it is at friend.amy:working", p.Refused[0].Why)
	assert.Equal(t, "no card nope is dealt to friend bob", p.Refused[1].Why)
	assert.Empty(t, p.Units)
}

func TestTakeAllUnstartedTakesEveryOneSheHasNotStarted(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", All: true, Started: map[string]string{"s1-2.w1": "her beat names it running"}}))
	amy := FriendRow("amy")
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col, "the started one stays with her")
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Working)+w.s.Fleet.Count(amy, Ready))
	assert.Empty(t, Check(w.s, nil))

	// a card that names her, taken from her, waits: the deal never gives it back to her
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	hd := Holder(running(w), w.s.Now, "s1-1")
	assert.Equal(t, HeldByWaiting, hd.By)
	assert.Contains(t, hd.Why, "taken back from amy, the friend its WHO line names")
}

func TestATakenCardIsDealtAgainToAnotherFriend(t *testing.T) {
	t.Parallel()
	w := takeWorld(t)
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-2"}, Reason: "bob is back"}))
	// amy has room again, and bob is down: it waits, never back to amy
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up}, FriendSeat{Name: "bob", Width: 1, Status: Down})
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col)
	mustHold(t, running(w), "s1-2", HeldByWaiting)

	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up}, FriendSeat{Name: "bob", Width: 1, Status: Up})
	wc := w.s.Fleet.Card("s1-2.w1")
	assert.Equal(t, FriendRow("bob"), wc.Row, "dealt to the other friend")
	assert.Equal(t, Working, wc.Col)
	assert.Equal(t, "3", wc.F("gen"), "the same card at its next generation: its own branch")
	assert.NotEmpty(t, wc.F("taken"))
	assert.Empty(t, wc.F(FieldTakenFrom))
	assert.Equal(t, Working, w.s.StateOf("s1-2"))
	assert.Equal(t, "s1-2.w1", w.s.Primary("s1-2").F("work"))
	assert.Equal(t, "1", w.s.Primary("s1-2").F("attempt"))
	assert.Nil(t, w.s.Fleet.Card("s1-2.w2"), "no second attempt")
	assert.Empty(t, Check(w.s, nil))
}

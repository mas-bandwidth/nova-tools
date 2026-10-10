package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dealt card handed back (docs/SPEC-SPRINT.md, a dealt card handed back).
// The attempt stays, the primary returns to ready, and the next pass does not
// deal it back to the friend it left (taken_from, friendsLeft).

func TestAHandedBackCardKeepsItsAttemptCountAndReturnsToThePool(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"}
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash"}
	dealWith(w, amy, bob)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	require.Equal(t, FriendRow("amy"), wc.Row)
	require.Equal(t, Ready, wc.Col, "dealt, and her lane has not started")
	require.Equal(t, "1", w.s.Primary("s1-1").F("attempt"))

	w.must(HandBack(w.s, HandBackReq{From: "friend.amy", IDs: []string{"s1-1"}, Reason: "eight ready cards are stranded", Who: "coordinator"}))
	wc = w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, Withdrawn, wc.Col)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, "eight ready cards are stranded", wc.F(FieldHandedBack))
	assert.Equal(t, FriendRow("amy"), wc.F(FieldTakenFrom))
	assert.Empty(t, wc.F(FieldTakeEnded), "a hand-back records no failure")
	assert.Empty(t, wc.F(FieldTakenBack))
	assert.Equal(t, "2", wc.F("gen"))
	assert.Equal(t, Ready, w.s.StateOf("s1-1"), "the primary is back in the pool")
	assert.Equal(t, "1", w.s.Primary("s1-1").F("attempt"), "a hand-back is no attempt")
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"))
	assert.Empty(t, Check(w.s, nil))

	dealWith(w, amy, bob)
	wc = w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, FriendRow("bob"), wc.Row, "the next pass does not deal it back to amy")
	assert.Equal(t, Ready, wc.Col)
	assert.Equal(t, "1", w.s.Primary("s1-1").F("attempt"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "no second attempt")
	assert.Empty(t, wc.F(FieldTakenFrom), "the redeal keeps her in friends_left, not taken_from")
	assert.Contains(t, wc.F(FieldFriendsLeft), "amy")
	assert.Empty(t, Check(w.s, nil))
}

func TestAHandedBackCardIsNotDealtStraightBackToTheOnlyFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"}
	dealWith(w, amy)
	w.must(HandBack(w.s, HandBackReq{From: "amy", IDs: []string{"s1-1.w1"}, Reason: "she has not started it"}))
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, "1", w.s.Primary("s1-1").F("attempt"))
	dealWith(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, Withdrawn, wc.Col, "the only friend is the one it left")
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
}

func TestAHandBackRefusesALaneThatHasStartedAndNamesIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"}
	dealStarted(w, amy)
	p := HandBack(w.s, HandBackReq{From: "amy", IDs: []string{"s1-1"}, Reason: "too late"})
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "s1-1", p.Refused[0].Key)
	assert.Contains(t, p.Refused[0].Why, "s1-1.w1 has started: lane her start")
	assert.Empty(t, p.Units)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)

	w.s.Fleet.Card("s1-2.w1").Fields["report"] = "Verdict: pending\n"
	p = HandBack(w.s, HandBackReq{From: "amy", IDs: []string{"s1-2"}, Reason: "too late", Lane: map[string]string{"s1-2.w1": "jobs/s1-2.w1"}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "lane jobs/s1-2.w1")
	assert.Empty(t, p.Units)
}

func TestHandBackAllUnstartedSkipsAStartedLane(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"})
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)
	w.must(HandBack(w.s, HandBackReq{From: "amy", All: true, Reason: "the rest can wait", Lane: map[string]string{}}))
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-1.w1").Col, "its progress is a started lane")
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, "1", w.s.Primary("s1-2").F("attempt"))
	assert.Equal(t, Ready, w.s.StateOf("s1-2"))
}

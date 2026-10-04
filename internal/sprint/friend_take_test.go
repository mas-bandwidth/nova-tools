package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card taken back (docs/SPEC-SPRINT.md section 1): a ready card taken frees
// no lane; a working card taken frees hers, and her oldest ready card not taken moves
// into working; the deal then cuts each one's next attempt; a pushed card, or one not
// on her row, is refused by name.
func TestFriendTakeFreesHerLaneAndTheDealCutsTheNextAttempt(t *testing.T) {
	t.Parallel()
	b := friendBrief("friend amy")
	w := friendWorld(t, b, b, b, b)
	amy := FriendRow("amy")
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
	require.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	require.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
	require.Equal(t, Ready, w.s.Fleet.Card("s1-4.w1").Col)

	p := FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1.w1"}, Pushed: map[string]string{"s1-1.w1": "abc"}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "s1-1.w1 has a push on its branch at abc")
	p = FriendTake(w.s, FriendTakeReq{Friend: "bob", IDs: []string{"s1-1"}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "s1-1 is no card dealt to friend bob")

	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-4"}, Who: "coordinator"}))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "a ready card taken frees no lane")
	assert.Equal(t, Ready, w.s.StateOf("s1-4"))

	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1.w1"}, Who: "coordinator"}))
	assert.Nil(t, w.s.Fleet.Placed("s1-1.w1"), "taken off her row")
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col, "her lane freed: her next is taken now")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	assert.Empty(t, Check(w.s, nil), "what is always true holds after a take")

	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
	assert.Equal(t, "s1-1.w2", w.s.Primary("s1-1").F("work"), "dealt again as its next attempt")
	assert.Equal(t, "s1-4.w2", w.s.Primary("s1-4").F("work"))
	assert.Equal(t, amy, w.s.Fleet.Card("s1-1.w2").Row)
	assert.Empty(t, Check(w.s, nil))
}

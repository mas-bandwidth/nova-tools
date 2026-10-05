package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card stays ready until she starts it (docs/SPEC-SPRINT.md section 1,
// friend-working-means-started.w1). The clock is the snapshot's, never a sleep.
func TestAFriendCardIsWorkingOnlyOnceTheFriendStartsIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	amy := FriendSeat{Name: "amy", Width: 3, Status: Up, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash,pro"}
	require.Equal(t, 20*time.Minute, w.s.FriendStartBound())

	dealWith(w, amy)
	row := FriendRow("amy")
	require.Equal(t, 3, w.s.Fleet.Count(row, Ready), "three dealt, lanes free")
	require.Equal(t, 0, w.s.Fleet.Count(row, Working))
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		wc := w.s.Fleet.Card(id)
		require.NotNil(t, wc)
		assert.Empty(t, wc.F("taken"), "%s is not taken at the deal", id)
		assert.Empty(t, wc.F(FieldFriendDeadline), "%s has no deadline yet", id)
		_, limit, _, _ := WorkDeadline(w.s, wc)
		assert.Equal(t, w.s.DealtMax(), limit, "%s is not on the working clock", id)
	}

	started := amy
	started.Running = []string{"s1-1.w1"}
	dealWith(w, started, bob)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, Working, wc.Col, "her beat names it running")
	assert.Equal(t, row, wc.Row)
	assert.Equal(t, stamp(w.s.Now), wc.F("taken"), "the deadline is stamped at the start")
	assert.Equal(t, stamp(w.s.Now), wc.F("first_taken"))
	field, limit, word, _ := WorkDeadline(w.s, wc)
	assert.Equal(t, "first_taken", field)
	assert.Equal(t, DeadlineUnfinished, limit)
	assert.Equal(t, "not finished", word)
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, row, w.s.Fleet.Card("s1-2.w1").Row, "she reports a running job: the others stay")

	w.tick(FriendStartBoundDefault)
	p := dealWith(w, started, bob)
	assert.Equal(t, row, w.s.Fleet.Card("s1-2.w1").Row, "past the bound, a live start keeps the unstarted cards")
	for _, u := range p.Units {
		assert.NotContains(t, u.Moved, "unstarted")
	}

	p = dealWith(w, amy, bob)
	moved := map[string]bool{}
	for _, u := range p.Units {
		if assert.NotContains(t, u.Moved, ";") {
			// one log line, not a second clause
		}
		if u.Key == "s1-2.w1" || u.Key == "s1-3.w1" {
			moved[u.Key] = true
			assert.Contains(t, u.Moved, "unstarted")
			assert.Contains(t, u.Moved, "bob")
			assert.NotContains(t, u.Moved, "\n")
		}
	}
	require.Equal(t, map[string]bool{"s1-2.w1": true, "s1-3.w1": true}, moved)
	for _, id := range []string{"s1-2.w1", "s1-3.w1"} {
		got := w.s.Fleet.Card(id)
		assert.Equal(t, FriendRow("bob"), got.Row, id)
		assert.Equal(t, Ready, got.Col, "levelled ready, not working")
		assert.Equal(t, "2", got.F("gen"))
		assert.Equal(t, "amy", got.F(FieldFriendsLeft))
		assert.Empty(t, got.F("taken"))
	}
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, row, w.s.Fleet.Card("s1-1.w1").Row)
	assert.Empty(t, Check(w.s, nil))
}

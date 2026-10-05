package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WHO preference admits a never-started card returned by friend take, while
// preserving the issued card and its attempt (docs/SPEC-SPRINT.md).
func TestUnpinUnstartedTakenBackCard(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"})
	wc := w.s.Fleet.Card("s1-2.w1")
	require.Equal(t, Ready, wc.Col)
	require.Empty(t, wc.F("taken"))
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-2"}, Reason: "share"}))
	brief := w.s.Primary("s1-2").F("brief")
	p := Unpin(w.s, UnpinReq{IDs: []string{"s1-2"}, Reason: "release", Who: "tester"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Units, 1)
	assert.Equal(t, "tester", p.Units[0].Notes[0].Who)
	assert.Contains(t, p.Units[0].Notes[0].What, "WHO: only friend amy")
	w.must(p)
	dealWith(w, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash"})
	wc = w.s.Fleet.Card("s1-2.w1")
	assert.Equal(t, "friend.bob", wc.Row)
	assert.Equal(t, 3, wc.Int("gen"))
	assert.Equal(t, 1, w.s.Primary("s1-2").Int("attempt"))
	assert.Nil(t, w.s.Fleet.Card("s1-2.w2"))
	assert.Equal(t, brief, w.s.Primary("s1-2").F("brief"))
}

func TestUnpinRefusesStartedOrFinishedWork(t *testing.T) {
	t.Parallel()
	for _, col := range []string{Working, Review, Merging, Landed} {
		t.Run(col, func(t *testing.T) {
			w := friendWorld(t, friendBrief("only friend amy"))
			dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up})
			c := w.s.Primary("s1-1")
			w.must(Plan{Units: []Unit{{Key: c.ID, Changes: []Change{change(Work, moveEntry(c, c.Row, col, nil))}}}})
			p := Unpin(w.s, UnpinReq{IDs: []string{c.ID}, Reason: "share"})
			assert.Empty(t, p.Units)
			assert.Len(t, p.Refused, 1)
			assert.Equal(t, "only.friend.amy", w.s.Primary(c.ID).F(FieldWho))
		})
	}
}

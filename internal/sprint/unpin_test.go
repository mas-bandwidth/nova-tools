package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnpinVerb(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("friend bob"), friendBrief("friend amy"))
	original := w.s.Primary("s1-1").F("brief")
	dealt, _, _ := friendDeal(w.s, []*Card{w.s.Primary("s1-3")}, []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}})
	w.must(dealt)
	w.s.Running = true
	p := Unpin(w.s, UnpinReq{Stream: "s1", Reason: "share work", Who: "tester"})
	require.Len(t, p.Units, 2)
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "s1-3", p.Refused[0].Key)
	assert.Contains(t, p.Units[0].Notes[0].What, "share work")
	assert.Contains(t, p.Units[0].Notes[0].What, "WHO: only friend amy")
	assert.Equal(t, "tester", p.Units[0].Notes[0].Who)
	assert.Equal(t, w.s.Now, p.Units[0].Notes[0].At)
	w.must(Plan{Units: p.Units})
	assert.Empty(t, w.s.Primary("s1-1").F(FieldWho))
	assert.Equal(t, original, w.s.Primary("s1-1").F("brief"))
	assert.NotEmpty(t, w.s.Primary("s1-3").F(FieldWho))
	assert.Len(t, Unpin(w.s, UnpinReq{IDs: []string{"s1-1"}}).Refused, 1)
	assert.Len(t, Unpin(w.s, UnpinReq{IDs: []string{"missing"}, Reason: "share"}).Refused, 1)
	assert.Len(t, Unpin(w.s, UnpinReq{IDs: []string{"s1-1"}, Reason: "again"}).Said, 1)
	dealWith(w)
	assert.Contains(t, []string{"m1", "m2"}, w.s.Fleet.Card("s1-1.w1").Row)
}

package sprint

import (
 "testing"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestColdReadUnpinUnstartedTakenBackCard(t *testing.T) {
 t.Parallel()
 w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
 dealWith(w, FriendSeat{Name:"amy", Width:1, Status:Up, Class:"flash"})
 wc := w.s.Fleet.Card("s1-2.w1")
 require.Equal(t, Ready, wc.Col)
 require.Empty(t, wc.F("taken"))
 w.must(FriendTake(w.s, FriendTakeReq{Friend:"amy", IDs:[]string{"s1-2"}, Reason:"share unstarted work"}))
 require.Equal(t, Ready, w.s.Primary("s1-2").Col)
 p := Unpin(w.s, UnpinReq{IDs:[]string{"s1-2"}, Reason:"release unstarted work", Who:"tester"})
 t.Logf("unpin refusals=%+v", p.Refused)
 assert.Empty(t, p.Refused, "an unstarted card returned to ready must permit dropping its WHO pin")
 assert.Len(t, p.Units, 1)
}

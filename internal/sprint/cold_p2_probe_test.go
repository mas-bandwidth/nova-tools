package sprint

import (
 "testing"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestColdP2ReturnedBoundary(t *testing.T) {
 t.Parallel()
 for _, mode := range []string{"returned", "ended", "redealt", "older-attempt", "no-take-back", "working-primary", "landed-primary", "fleet-row", "ready-work"} {
  t.Run(mode, func(t *testing.T) {
   w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
   dealWith(w, FriendSeat{Name:"amy", Width:1, Status:Up, Class:"flash"})
   w.must(FriendTake(w.s, FriendTakeReq{Friend:"amy", IDs:[]string{"s1-2"}, Reason:"share"}))
   pr, wc := w.s.Primary("s1-2"), w.s.Fleet.Card("s1-2.w1")
   brief := pr.F("brief")
   var changes []Change
   switch mode {
   case "ended": changes = append(changes, change(Fleet, setEntry(wc, map[string]string{FieldTakeEnded:stamp(w.s.Now)})))
   case "redealt": changes = append(changes, change(Fleet, setEntry(wc, map[string]string{"redeals":"1"})))
   case "older-attempt": changes = append(changes, change(Work, setEntry(pr, map[string]string{"attempt":"2"})))
   case "no-take-back": changes = append(changes, change(Fleet, setEntry(wc, nil, FieldTakenBack)))
   case "working-primary": changes = append(changes, change(Work, moveEntry(pr, pr.Row, Working, nil)))
   case "landed-primary": changes = append(changes, change(Work, moveEntry(pr, pr.Row, Landed, nil)))
   case "fleet-row": changes = append(changes, change(Fleet, moveEntry(wc, "m1", Withdrawn, nil)))
   case "ready-work": changes = append(changes, change(Fleet, moveEntry(wc, wc.Row, Ready, nil)))
   }
   if len(changes) > 0 { w.must(Plan{Units:[]Unit{{Key:pr.ID, Changes:changes}}}) }
   p := Unpin(w.s, UnpinReq{IDs:[]string{pr.ID}, Reason:"share", Who:"reviewer"})
   if mode != "returned" {
    assert.Empty(t, p.Units)
    assert.Len(t, p.Refused, 1)
    assert.Equal(t, "only.friend.amy", w.s.Primary(pr.ID).F(FieldWho))
    return
   }
   require.Empty(t, p.Refused)
   require.Len(t, p.Units, 1)
   require.Len(t, p.Units[0].Notes, 1)
   assert.Equal(t, "reviewer", p.Units[0].Notes[0].Who)
   w.must(p)
   dealWith(w, FriendSeat{Name:"bob", Width:1, Status:Up, Class:"flash"})
   assert.Equal(t, "friend.bob", w.s.Fleet.Card(wc.ID).Row)
   assert.Equal(t, 3, w.s.Fleet.Card(wc.ID).Int("gen"))
   assert.Equal(t, 1, w.s.Primary(pr.ID).Int("attempt"))
   assert.Equal(t, brief, w.s.Primary(pr.ID).F("brief"))
   assert.Empty(t, w.s.Primary(pr.ID).F(FieldWho))
  })
 }
}

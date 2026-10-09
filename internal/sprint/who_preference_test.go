package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WHO: friend (any) is a preference among friends; WHO: friend <name> and WHO: only friend
// <name> name her, and the card waits for her alone (the-dealer-honors-who.w1).
func TestWhoIsAPreference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, who, want string
		seats           []FriendSeat
	}{
		{"unpinned", "", "friend.bob", []FriendSeat{{Name: "bob", Width: 1, Status: Up, Class: "pro,heavy"}}},
		{"preferred first", "friend amy", "friend.amy", []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "pro"}, {Name: "bob", Width: 8, Status: Up, Class: "pro"}}},
		{"named full waits", "friend amy", "", []FriendSeat{{Name: "amy", Width: 0, Status: Up, Class: "pro"}, {Name: "bob", Width: 1, Status: Up, Class: "pro"}}},
		{"named never to the fleet", "friend amy", "", []FriendSeat{{Name: "amy", Width: 0, Status: Up, Class: "pro"}, {Name: "bob", Width: 1, Status: Up, Class: "flash"}}},
		{"only waits", "only friend amy", "", []FriendSeat{{Name: "amy", Width: 0, Status: Up, Class: "pro"}, {Name: "bob", Width: 1, Status: Up, Class: "pro"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brief := "c: work tier: pro\n"
			if tc.who != "" {
				brief += "WHO: " + tc.who + "\n"
			}
			w := friendWorld(t, brief+"\nThe task.")
			dealWith(w, tc.seats...)
			wc := w.s.Fleet.Card("s1-1.w1")
			if tc.want == "" {
				assert.Nil(t, wc)
				assert.Equal(t, Ready, w.s.StateOf("s1-1"))
				assert.Contains(t, Holder(running(w), w.s.Now, "s1-1").Why, "waits for "+tc.who)
				return
			}
			require.NotNil(t, wc)
			if tc.want == "fleet" {
				assert.Contains(t, []string{"m1", "m2"}, wc.Row)
			} else {
				assert.Equal(t, tc.want, wc.Row)
			}
		})
	}
}

// A first deal is hers alone (the-dealer-honors-whob.w5). WHO: friend <name> on a card
// never dealt is a true-ownership pin, the same as a rework of it: while she is down it
// waits ready, and another friend up with room is not dealt it.
func TestAFirstDealIsHersAlone(t *testing.T) {
	t.Parallel()
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, amy, bob)
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"), "amy down: it waits for her, never to bob")
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Zero(t, w.s.Fleet.Count(FriendRow("bob"), Ready)+w.s.Fleet.Count(FriendRow("bob"), Working))
}

// Changing to a subscription friend must not reset a failed take's retry
// count or bypass the existing bound/escalation decision.
func TestPreferencePreservesTheRetryBound(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, MaxRedeals} {
		t.Run(itoa(n), func(t *testing.T) {
			w := friendWorld(t, "job tier: pro\n\nWork.")
			w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
			wc := w.s.Fleet.Card("s1-1.w1")
			pr := w.s.Primary("s1-1")
			w.must(Plan{Units: []Unit{{Key: pr.ID, Changes: []Change{
				change(Fleet, moveEntry(wc, wc.Row, Withdrawn, map[string]string{"redeals": itoa(n), FieldTakeEnded: stamp(w.s.Now)})),
				change(Work, moveEntry(pr, pr.Row, Ready, nil)),
			}}}})
			p := FriendDeal(w.s, []*Card{w.s.Primary(pr.ID)}, []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "pro"}})
			if n == MaxRedeals {
				assert.Empty(t, p.Units)
				return
			}
			w.must(p)
			wc = w.s.Fleet.Card(wc.ID)
			assert.Equal(t, "friend.amy", wc.Row)
			assert.Equal(t, n+1, wc.Int("redeals"))
			assert.Empty(t, wc.F(FieldTakeEnded))
		})
	}
}

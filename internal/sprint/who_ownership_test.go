package sprint

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// docs/SPEC-SPRINT.md: a named WHO is an ownership pin.
func TestACardWithWhoIsDealtOnlyToThatFriend(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, status string
		width        int
		want         bool
	}{
		{"up with room", Up, 1, true}, {"down", Down, 1, false}, {"held", Held, 1, false}, {"full", Up, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := friendWorld(t, "c: work tier: pro\nWHO: friend amy\n\nThe task.")
			seats := []FriendSeat{{Name: "amy", Width: tc.width, Status: tc.status, Class: "pro"}, {Name: "bob", Width: 8, Status: Up, Class: "pro"}}
			w.s.Friends = seats
			dealWith(w, seats...)
			wc := w.s.Fleet.Card("s1-1.w1")
			if tc.want {
				require.NotNil(t, wc)
				assert.Equal(t, FriendRow("amy"), wc.Row)
				return
			}
			assert.Nil(t, wc)
			assert.Equal(t, Ready, w.s.StateOf("s1-1"))
			held := Holder(running(w), w.s.Now, "s1-1")
			assert.Equal(t, HeldByWaiting, held.By)
			assert.Contains(t, held.Why, "friend amy")
			assert.NotEmpty(t, Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}).Refused)
		})
	}
}

func TestFriendTakePlacesAnUntakenCardOnTheNamedRow(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"ready", "other row", "taken"} {
		t.Run(state, func(t *testing.T) {
			w := friendWorld(t, friendBrief("friend"))
			if state != "ready" {
				dealWith(w, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash"})
			}
			if state == "taken" {
				startLanes(w, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash"})
			}
			p := FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1"}, Assign: true})
			if state == "taken" {
				require.Len(t, p.Refused, 1)
				assert.Contains(t, p.Refused[0].Why, "lane")
				assert.Contains(t, p.Refused[0].Why, "friend.bob")
				return
			}
			require.Empty(t, p.Refused)
			w.must(p)
			wc := w.s.Fleet.Card("s1-1.w1")
			require.NotNil(t, wc)
			assert.Equal(t, FriendRow("amy"), wc.Row)
			assert.Equal(t, Ready, wc.Col)
		})
	}
}

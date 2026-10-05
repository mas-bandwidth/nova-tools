package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One tier resolution decides every friend decision (docs/SPEC-SPRINT.md section 1,
// friend-deal-one-tier.w2): the card's tier now, the dealer's default when it names none
// (CardTier). The deal, the level, the take back of a re-tiered card and the packet a
// friend's lane reads all read it, so no deal is taken back by a lane that read another
// tier, a WHO friend is preferred only among the friends serving the tier, and a friend's
// whole tier list is never one class.
func TestEveryFriendDecisionReadsTheOneTierOfTheCard(t *testing.T) {
	t.Parallel()
	flash := []string{cardhdr.RouteFlash}
	plain := func(name string) string { return "c: " + name + "\nREPO: mas-bandwidth/nova-tools\n\nThe task." }

	t.Run("an untiered card's packet carries the default tier", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, plain("untiered"))
		pr := w.s.Primary("s1-1")
		require.Equal(t, DefaultTier, CardTier(pr), "a card that names no tier is on the dealer's default")
		dealWith(w, FriendSeat{Name: "freddy", Width: 1, Status: Up, Tiers: flash})
		wc := w.s.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc)
		require.Equal(t, FriendRow("freddy"), wc.Row, "flash, the default, is a tier freddy serves")
		p := PacketOf("sprint", 0, wc, w.s.Primary("s1-1"), nil, nil)
		assert.Equal(t, cardhdr.RouteFlash, p.Tier, "the lane reads the tier the deal resolved, never a dash off the brief")

		// the tick after: freddy's card stays; no deal and take-back loop
		dealWith(w, FriendSeat{Name: "freddy", Width: 1, Status: Up, Tiers: flash})
		wc = w.s.Fleet.Card("s1-1.w1")
		assert.Equal(t, FriendRow("freddy"), wc.Row)
		assert.Equal(t, 1, wc.Int("gen"), "never taken back and dealt again")
	})

	t.Run("a re-tiered card leaves a holder who cannot serve it", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, plain("one"), plain("two"))
		emma := FriendSeat{Name: "emma", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}}
		dealWith(w, emma)
		require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
		require.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col, "room 2: one working, one ready behind")

		// both re-tiered heavy after the deal (brief --tier); she has started the working one
		w.must(Brief(w.s, BriefReq{ID: "s1-1", Tier: cardhdr.RouteHeavy}))
		w.must(Brief(w.s, BriefReq{ID: "s1-2", Tier: cardhdr.RouteHeavy}))
		emma.Running = []string{"s1-1.w1"}
		heavy := FriendSeat{Name: "hana", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteHeavy}}
		dealWith(w, emma, heavy)
		assert.Equal(t, FriendRow("emma"), w.s.Fleet.Card("s1-1.w1").Row, "a started card stays with her and finishes")
		taken := w.s.Fleet.Card("s1-2.w1")
		require.Equal(t, Withdrawn, taken.Col, "the unstarted card is taken back")
		assert.Contains(t, taken.F(FieldTakenBack), "heavy")
		assert.Equal(t, Ready, w.s.StateOf("s1-2"))
		assert.Empty(t, Check(w.s, nil))

		// the next tick deals it again, to a friend serving heavy, and it stays there
		dealWith(w, emma, heavy)
		again := w.s.Fleet.Card("s1-2.w1")
		require.Equal(t, FriendRow("hana"), again.Row)
		p := PacketOf("sprint", 0, again, w.s.Primary("s1-2"), nil, nil)
		assert.Equal(t, cardhdr.RouteHeavy, p.Tier)
		gen := again.Int("gen")
		dealWith(w, emma, heavy)
		assert.Equal(t, gen, w.s.Fleet.Card("s1-2.w1").Int("gen"), "dealt once, never taken back again")
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("a WHO friend not serving the tier is skipped", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, "c: split work tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: friend alex\n\nThe task.")
		alex := FriendSeat{Name: "alex", Width: 4, Status: Up, Tiers: flash}
		johnny := FriendSeat{Name: "johnny", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}}
		dealWith(w, alex, johnny)
		assert.Equal(t, FriendRow("johnny"), w.s.Fleet.Card("s1-1.w1").Row, "alex is named but serves no pro: a preference skipped")

		// the preference holds only among the friends serving the tier
		lanes, free := map[string]int{"amy": 0, "bob": 2}, map[string]int{"amy": 1, "bob": 4}
		assert.Equal(t, "amy", preferredFriend([]string{"amy", "bob"}, "amy", lanes, free), "named and serving: preferred")
		assert.Equal(t, "bob", preferredFriend([]string{"bob"}, "amy", lanes, free), "named, not serving: never a pin past the tier")
	})

	t.Run("a flash card levels between friends whose tier lists differ", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, plain("one"), plain("two"))
		amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
		dealWith(w, amy, FriendSeat{Name: "bob", Width: 1, Status: Held, Class: "flash,frontier,heavy,pro"})
		ready := w.s.Fleet.Cell(FriendRow("amy"), Ready)
		require.Len(t, ready, 1)
		dealWith(w, amy, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash,frontier,heavy,pro"})
		assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card(ready[0].ID).Row, "a flash card is levelled by its tier, never by a class string")

		// the attempt cap's friend is chosen by her tiers too, never her whole list as one class
		seats := []FriendSeat{{Name: "bob", Width: 2, Status: Up, Class: "flash,frontier,heavy,pro"}}
		assert.Equal(t, "bob", friendWithFree(seats, map[string]int{"bob": 2}, cardhdr.RouteFrontier, cardhdr.RouteHeavy))
	})
}

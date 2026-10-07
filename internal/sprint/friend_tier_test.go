package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every friend decision reads the one tier of the card (Snapshot.DealTier; docs/SPEC-SPRINT.md
// section 1, "One tier for every friend decision", friend-deal-one-tier-bb.w2): the deal, the
// level, the take back of a re-tiered card and the packet's tier all name the same tier, so no
// two of them can disagree about a card.
func TestEveryFriendDecisionReadsTheOneTierOfTheCard(t *testing.T) {
	t.Parallel()
	proBrief := func(who string) string {
		return "c: a pro card tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
	}

	t.Run("a card with no tier carries the dealer's default in its packet", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend"))
		dealWith(w, FriendSeat{Name: "fay", Width: 2, Status: Up, Tiers: []string{"flash"}})
		wc := w.s.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc)
		pr := w.s.Primary("s1-1")
		assert.Equal(t, "flash", w.s.DealTier(pr))
		assert.Equal(t, "flash", DealtTier(wc, pr))
		assert.Equal(t, "flash", w.s.DealTier(nil), "no primary is the dealer's default")
	})

	t.Run("a WHO friend who does not serve the tier holds its named card ready", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, proBrief("friend alex"))
		dealWith(w,
			FriendSeat{Name: "alex", Width: 2, Status: Up, Tiers: []string{"flash"}},
			FriendSeat{Name: "jo", Width: 2, Status: Up, Tiers: []string{"flash", "pro"}})
		assert.Nil(t, w.s.Fleet.Card("s1-1.w1"), "a named WHO never falls back to jo")
		assert.Equal(t, Ready, w.s.StateOf("s1-1"))
		assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("alex"), Working)+w.s.Fleet.Count(FriendRow("alex"), Ready))
	})

	t.Run("a flash card levels between friends whose tier lists differ", func(t *testing.T) {
		t.Parallel()
		briefs := []string{friendBrief("friend amy")}
		for range 6 {
			briefs = append(briefs, friendBrief("friend"))
		}
		w := friendWorld(t, briefs...)
		dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash", "pro"}})
		seats := []FriendSeat{
			{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash", "pro"}},
			{Name: "bob", Width: 2, Status: Up, Tiers: []string{"flash", "frontier", "heavy", "pro"}},
		}
		p := w.must(FriendLevel(w.s, FriendLevelReq{Seats: seats}))
		require.NotEmpty(t, p.Units, "bob serves flash too: the cards level to him")
		assert.Equal(t, len(p.Units), w.s.Fleet.Count(FriendRow("bob"), Ready)+w.s.Fleet.Count(FriendRow("bob"), Working), "every card moved is on his row")
	})

	t.Run("a re-tiered card leaves a holder who cannot serve it and is dealt again", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
		emma := FriendSeat{Name: "emma", Width: 1, Status: Up, Tiers: []string{"flash", "pro"}}
		dealWith(w, emma)
		require.Equal(t, FriendRow("emma"), w.s.Fleet.Card("s1-1.w1").Row)
		require.Equal(t, FriendRow("emma"), w.s.Fleet.Card("s1-2.w1").Row)
		w.must(Brief(w.s, BriefReq{ID: "s1-1", Tier: "heavy"}))
		w.must(Brief(w.s, BriefReq{ID: "s1-2", Tier: "heavy"}))
		require.Equal(t, "heavy", w.s.DealTier(w.s.Primary("s1-1")))

		// no friend serves heavy: both come back to ready, none stranded on her row
		p, _ := TickDeal(w.s, TickReq{Friends: []FriendSeat{emma}})
		w.must(p)
		for _, id := range []string{"s1-1", "s1-2"} {
			wc := w.s.Fleet.Card(id + ".w1")
			require.NotNil(t, wc)
			assert.Equal(t, Withdrawn, wc.Col, "%s taken back", id)
			assert.Equal(t, Ready, w.s.StateOf(id))
		}

		// a friend serving heavy takes them
		hal := FriendSeat{Name: "hal", Width: 2, Status: Up, Tiers: []string{"heavy"}}
		p, _ = TickDeal(w.s, TickReq{Friends: []FriendSeat{emma, hal}})
		w.must(p)
		assert.Equal(t, FriendRow("hal"), w.s.Fleet.Card("s1-1.w1").Row)
		assert.Equal(t, FriendRow("hal"), w.s.Fleet.Card("s1-2.w1").Row)
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("a started card stays", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend"))
		emma := FriendSeat{Name: "emma", Width: 1, Status: Up, Tiers: []string{"flash", "pro"}, Running: []string{"s1-1.w1"}}
		dealWith(w, emma)
		w.must(Brief(w.s, BriefReq{ID: "s1-1", Tier: "heavy"}))
		p, _ := TickDeal(w.s, TickReq{Friends: []FriendSeat{emma}})
		w.must(p)
		assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	})
}

package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

func TestCollectCardsView(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}

	// Add primaries to Work table:
	// c1: stream s1, col Review, tier flash (explicit)
	c1 := &Card{
		ID:     "s1-1",
		Row:    "s1",
		Col:    string(Review),
		Score:  1,
		Fields: map[string]string{"brief": "s1: task 1 (s1) tier: flash", "attempt": "1"},
	}
	// c2: stream s1, col Review, tier pro
	c2 := &Card{
		ID:     "s1-2",
		Row:    "s1",
		Col:    string(Review),
		Score:  2,
		Fields: map[string]string{"brief": "s1: task 2 (s1) tier: pro", "attempt": "1"},
	}
	// c3: stream s1, col Review, default tier (no brief) -> flash
	c3 := &Card{
		ID:     "s1-3",
		Row:    "s1",
		Col:    string(Review),
		Score:  3,
		Fields: map[string]string{"attempt": "1"},
	}
	// c4: stream s2, col Working, tier heavy
	c4 := &Card{
		ID:     "s2-1",
		Row:    "s2",
		Col:    string(Working),
		Score:  1,
		Fields: map[string]string{"brief": "s2: task 1 (s2) tier: heavy", "attempt": "1"},
	}
	// c5: stream s1, col Ready, tier frontier
	c5 := &Card{
		ID:     "s1-4",
		Row:    "s1",
		Col:    string(Ready),
		Score:  4,
		Fields: map[string]string{"brief": "s1: task 4 (s1) tier: frontier", "attempt": "0"},
	}
	// sentinel (should be excluded)
	sentinel := &Card{
		ID:     "s1-stop",
		Row:    "s1",
		Col:    string(Waiting),
		Score:  5,
		Fields: map[string]string{"kind": "sentinel"},
	}

	s.Work.Put(c1)
	s.Work.Put(c2)
	s.Work.Put(c3)
	s.Work.Put(c4)
	s.Work.Put(c5)
	s.Work.Put(sentinel)

	// Work cards in Fleet:
	// c1 and c2 held by m1, c3 held by friend.amy, c4 held by m2
	wc1 := &Card{
		ID:     WorkCardID("s1-1", 1),
		Row:    "m1",
		Col:    string(DoneOK),
		Fields: map[string]string{"primary": "s1-1", "member": "m1"},
	}
	wc2 := &Card{
		ID:     WorkCardID("s1-2", 1),
		Row:    "m1",
		Col:    string(DoneOK),
		Fields: map[string]string{"primary": "s1-2", "member": "m1"},
	}
	wc3 := &Card{
		ID:     WorkCardID("s1-3", 1),
		Row:    FriendRow("amy"),
		Col:    string(DoneOK),
		Fields: map[string]string{"primary": "s1-3", "member": FriendRow("amy")},
	}
	wc4 := &Card{
		ID:     WorkCardID("s2-1", 1),
		Row:    "m2",
		Col:    string(Working),
		Fields: map[string]string{"primary": "s2-1", "member": "m2"},
	}

	s.Fleet.Put(wc1)
	s.Fleet.Put(wc2)
	s.Fleet.Put(wc3)
	s.Fleet.Put(wc4)

	t.Run("by tier with review filter", func(t *testing.T) {
		res := CollectCardsView(s, CardsViewFilter{Col: string(Review)}, "tier")
		assert.Equal(t, 3, res.Total)
		assert.Equal(t, 2, res.Counts[cardhdr.RouteFlash])
		assert.Equal(t, 1, res.Counts[cardhdr.RoutePro])
		assert.Zero(t, res.Counts[cardhdr.RouteHeavy])
	})

	t.Run("by col all cards", func(t *testing.T) {
		res := CollectCardsView(s, CardsViewFilter{}, "col")
		assert.Equal(t, 5, res.Total, "sentinel is excluded")
		assert.Equal(t, 3, res.Counts[string(Review)])
		assert.Equal(t, 1, res.Counts[string(Working)])
		assert.Equal(t, 1, res.Counts[string(Ready)])
	})

	t.Run("by stream all cards", func(t *testing.T) {
		res := CollectCardsView(s, CardsViewFilter{}, "stream")
		assert.Equal(t, 5, res.Total)
		assert.Equal(t, 4, res.Counts["s1"])
		assert.Equal(t, 1, res.Counts["s2"])
	})

	t.Run("by holder with review filter", func(t *testing.T) {
		res := CollectCardsView(s, CardsViewFilter{Col: string(Review)}, "holder")
		assert.Equal(t, 3, res.Total)
		assert.Equal(t, 2, res.Counts["m1"])
		assert.Equal(t, 1, res.Counts[FriendRow("amy")])
	})

	t.Run("filter by holder matches friend row or name", func(t *testing.T) {
		res1 := CollectCardsView(s, CardsViewFilter{Holder: "amy"}, "")
		require.Len(t, res1.Cards, 1)
		assert.Equal(t, "s1-3", res1.Cards[0].ID)

		res2 := CollectCardsView(s, CardsViewFilter{Holder: FriendRow("amy")}, "")
		require.Len(t, res2.Cards, 1)
		assert.Equal(t, "s1-3", res2.Cards[0].ID)

		res3 := CollectCardsView(s, CardsViewFilter{Holder: "m1"}, "")
		require.Len(t, res3.Cards, 2)
	})

	t.Run("list without by in work order", func(t *testing.T) {
		res := CollectCardsView(s, CardsViewFilter{Col: string(Review)}, "")
		assert.Equal(t, 3, res.Total)
		require.Len(t, res.Cards, 3)
		assert.Equal(t, "s1-1", res.Cards[0].ID)
		assert.Equal(t, "s1-2", res.Cards[1].ID)
		assert.Equal(t, "s1-3", res.Cards[2].ID)
	})
}

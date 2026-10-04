package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Dealing respects a friend's tier and mode (docs/SPEC-SPRINT.md section 1, a friend's card):
// a friend with a tier gets only cards at or below it; a one-shot friend gets one card at a time;
// the twin store test covers both.

var testT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func tierModeBrief(who, tier string) string {
	return fmt.Sprintf("c: a friend's card tier: %s\nREPO: mas-bandwidth/nova-tools\nWHO: %s\n\nThe task.", tier, who)
}

func friendSnapshot(readers ...string) *sprint.Snapshot {
	s := &sprint.Snapshot{
		Now:         testT0,
		Work:        sprint.NewTable(sprint.Work),
		Readers:     sprint.NewTable(sprint.Readers),
		Merge:       sprint.NewTable(sprint.Merge),
		Fleet:       sprint.NewTable(sprint.Fleet),
		Coordinator: "coordinator",
		Actor:       "coordinator",
	}
	s.Readers.SetRows(append(s.Readers.Rows(), readers...))
	return s
}

// TestDealingRespectsAFriendTierAndMode pins the rules of friend dealing:
// 1. A friend with a tier gets only cards at or below it (flash < pro < frontier).
// 2. A one-shot friend gets one card at a time (capacity of 1, whatever her width).
// 3. The twin store test covers both tier and mode on store.Mem.
func TestDealingRespectsAFriendTierAndMode(t *testing.T) {
	t.Parallel()

	t.Run("tier ceiling: a friend gets only cards at or below her tier", func(t *testing.T) {
		t.Parallel()
		s := friendSnapshot()
		s.Work.SetRows([]string{"s1"})

		cards := []*sprint.Card{
			{ID: "s1-1", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend", "flash"), sprint.FieldWho: sprint.WhoFriend}},
			{ID: "s1-2", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend", "pro"), sprint.FieldWho: sprint.WhoFriend}},
			{ID: "s1-3", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend", "frontier"), sprint.FieldWho: sprint.WhoFriend}},
			{ID: "s1-4", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend amy", "pro"), sprint.FieldWho: sprint.FriendRow("amy")}},
		}
		for _, c := range cards {
			s.Work.Put(c)
		}

		seats := []sprint.FriendSeat{
			{Name: "amy", Width: 4, Status: sprint.Up, Tier: "flash"},
			{Name: "bob", Width: 4, Status: sprint.Up, Tier: "pro"},
			{Name: "carla", Width: 4, Status: sprint.Up, Tier: "frontier"},
		}

		p := sprint.FriendDeal(s, cards, seats)

		// s1-4 names amy, but is pro; amy's tier is flash: cannot be dealt, waits ready
		for _, u := range p.Units {
			assert.NotEqual(t, "s1-4", u.Key, "a pro card naming amy is not dealt to amy whose tier is flash")
		}

		// Check card placements in the plan units
		unitByCard := map[string]sprint.Unit{}
		for _, u := range p.Units {
			unitByCard[u.Key] = u
		}

		// s1-1 is flash: can go to amy (most room / first by name among equals)
		u1, ok1 := unitByCard["s1-1"]
		require.True(t, ok1, "s1-1 flash card is dealt")
		assert.Equal(t, sprint.FriendRow("amy"), u1.Changes[0].Entry.Create.Row)

		// s1-2 is pro: cannot go to amy; goes to bob
		u2, ok2 := unitByCard["s1-2"]
		require.True(t, ok2, "s1-2 pro card is dealt")
		assert.Equal(t, sprint.FriendRow("bob"), u2.Changes[0].Entry.Create.Row)

		// s1-3 is frontier: cannot go to amy or bob; goes to carla
		u3, ok3 := unitByCard["s1-3"]
		require.True(t, ok3, "s1-3 frontier card is dealt")
		assert.Equal(t, sprint.FriendRow("carla"), u3.Changes[0].Entry.Create.Row)
	})

	t.Run("one-shot mode: a one-shot friend gets one card at a time", func(t *testing.T) {
		t.Parallel()
		s := friendSnapshot()
		s.Work.SetRows([]string{"s1"})

		cards := []*sprint.Card{
			{ID: "s1-1", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend dan", "flash"), sprint.FieldWho: sprint.FriendRow("dan")}},
			{ID: "s1-2", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend dan", "flash"), sprint.FieldWho: sprint.FriendRow("dan")}},
			{ID: "s1-3", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend dan", "flash"), sprint.FieldWho: sprint.FriendRow("dan")}},
			{ID: "s1-4", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend eva", "flash"), sprint.FieldWho: sprint.FriendRow("eva")}},
			{ID: "s1-5", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend eva", "flash"), sprint.FieldWho: sprint.FriendRow("eva")}},
			{ID: "s1-6", Row: "s1", Col: sprint.Ready, Fields: map[string]string{"brief": tierModeBrief("friend eva", "flash"), sprint.FieldWho: sprint.FriendRow("eva")}},
		}
		for _, c := range cards {
			s.Work.Put(c)
		}

		seats := []sprint.FriendSeat{
			{Name: "dan", Width: 4, Status: sprint.Up, Mode: "one-shot"},
			{Name: "eva", Width: 4, Status: sprint.Up, Mode: "batch"},
		}

		p := sprint.FriendDeal(s, cards, seats)

		danDealt := 0
		evaDealt := 0
		for _, u := range p.Units {
			if u.Changes[0].Entry.Create.Row == sprint.FriendRow("dan") {
				danDealt++
			}
			if u.Changes[0].Entry.Create.Row == sprint.FriendRow("eva") {
				evaDealt++
			}
		}

		assert.Equal(t, 1, danDealt, "one-shot friend dan gets only 1 card at a time despite width 4")
		assert.Equal(t, 3, evaDealt, "batch friend eva gets all 3 cards up to her width 4")
	})

	t.Run("twin store: friend tier and mode on store.Mem", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		m := store.NewMem()
		now := testT0
		var mu sync.Mutex
		n := 0
		st := &store.Store{
			B:     m,
			Names: sprint.Names{Prefix: "t-"},
			Actor: "coordinator",
			Now:   func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
			NewID: func() string { mu.Lock(); defer mu.Unlock(); n++; return fmt.Sprint(n) },
			Sleep: func(time.Duration) {},
		}
		require.NoError(t, st.Init(ctx))
		require.NoError(t, m.SetCoordinator(ctx, "coordinator"))

		// Sync friends with tier and mode
		specs := []store.FriendSpec{
			{Name: "carla", Width: 4, Tier: "flash", Mode: "one-shot"},
			{Name: "dan", Width: 4, Tier: "pro", Mode: "batch"},
		}
		added, _, _, err := st.SyncFriends(ctx, specs)
		require.NoError(t, err)
		assert.Equal(t, []string{"carla", "dan"}, added)

		// Heartbeat to make them up
		_, err = st.FriendBeat(ctx, "carla")
		require.NoError(t, err)
		_, err = st.FriendBeat(ctx, "dan")
		require.NoError(t, err)

		rows, err := st.FriendRows(ctx, now)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, "flash", rows[0].Tier)
		assert.Equal(t, "one-shot", rows[0].Mode)
		assert.Equal(t, "pro", rows[1].Tier)
		assert.Equal(t, "batch", rows[1].Mode)

		// Add cards
		cardAdds := []sprint.CardAdd{
			{ID: "s1-1", Brief: tierModeBrief("friend carla", "pro")},   // pro card for carla (flash) -> should wait
			{ID: "s1-2", Brief: tierModeBrief("friend carla", "flash")}, // flash card 1 for carla -> dealt
			{ID: "s1-3", Brief: tierModeBrief("friend carla", "flash")}, // flash card 2 for carla -> waits (one-shot)
			{ID: "s1-4", Brief: tierModeBrief("friend dan", "pro")},     // pro card for dan -> dealt
			{ID: "s1-5", Brief: tierModeBrief("friend dan", "flash")},   // flash card for dan -> dealt
		}
		_, err = st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "s1", Cards: cardAdds}))
		require.NoError(t, err)

		// Start machine and run tick deal
		_, _, _, err = st.SetMachine(ctx, true)
		require.NoError(t, err)

		_, err = st.Tick(ctx)
		require.NoError(t, err)

		// carla is one-shot and flash:
		// s1-1 (pro) is not dealt
		// s1-2 (flash) is dealt
		// s1-3 (flash) is not dealt yet (one-shot allows only 1 card)
		c1, err := st.CardOf(ctx, "s1-1")
		require.NoError(t, err)
		assert.Equal(t, sprint.Ready, c1.Primary.Col, "pro card for carla waits ready")

		c2, err := st.CardOf(ctx, "s1-2")
		require.NoError(t, err)
		assert.Equal(t, sprint.Working, c2.Primary.Col, "flash card 1 for carla is working")
		require.Len(t, c2.Work, 1)
		assert.Equal(t, sprint.FriendRow("carla"), c2.Work[0].Row)

		c3, err := st.CardOf(ctx, "s1-3")
		require.NoError(t, err)
		assert.Equal(t, sprint.Ready, c3.Primary.Col, "flash card 2 for carla waits ready (one-shot)")

		// dan is batch and pro:
		// s1-4 (pro) is dealt
		// s1-5 (flash) is dealt
		c4, err := st.CardOf(ctx, "s1-4")
		require.NoError(t, err)
		assert.Equal(t, sprint.Working, c4.Primary.Col, "pro card for dan is working")
		require.Len(t, c4.Work, 1)
		assert.Equal(t, sprint.FriendRow("dan"), c4.Work[0].Row)

		c5, err := st.CardOf(ctx, "s1-5")
		require.NoError(t, err)
		assert.Equal(t, sprint.Working, c5.Primary.Col, "flash card for dan is working")
		require.Len(t, c5.Work, 1)
		assert.Equal(t, sprint.FriendRow("dan"), c5.Work[0].Row)
	})
}

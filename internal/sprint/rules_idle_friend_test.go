package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store/storetest"
)

func TestFriendIdle(t *testing.T) {
	t.Parallel()

	s := storetest.NewSnap(t)
	// Add a friend row
	s.Work.SetRows(append(s.Work.Rows(), "friend"))

	// Set up a friend with cards
	f := FriendSeat{
		Name:   "friend",
		Width:  2,
		Status: Up,
		Active: time.Now().Add(-30 * time.Minute),
	}
	s.Friends = append(s.Friends, f)

	// Add a work card with progress evidence
	now := time.Now()
	oldEvidence := now.Add(-30 * time.Minute)
	c := &Card{ID: "card1", Col: Ready, Row: "friend"}
	c.Set(FieldTakenBy, "friend")
	c.Set(FieldProgress, stamp(oldEvidence))
	s.Work.Table["friend"] = append(s.Work.Table["friend"], c)

	// Friend should be idle with 20m bound
	idleAfter := 20 * time.Minute
	loaded, newest := IsIdleLoaded(s, f, idleAfter)
	require.True(t, loaded)
	require.Equal(t, oldEvidence, newest)

	// Reset evidence to be recent
	newEvidence := now.Add(-10 * time.Minute)
	c.Set(FieldProgress, stamp(newEvidence))

	// Friend should not be idle
	loaded, newest = IsIdleLoaded(s, f, idleAfter)
	require.False(t, loaded)
}

func TestTickRuleFriendIdle(t *testing.T) {
	t.Parallel()

	s := storetest.NewSnap(t)
	s.Work.SetRows(append(s.Work.Rows(), "friend"))

	now := time.Now()
	idleAfter := 20 * time.Minute

	// Friend with stale evidence
	f := FriendSeat{
		Name:   "friend",
		Width:  2,
		Status: Up,
		Active: now.Add(-40 * time.Minute),
	}
	s.Friends = append(s.Friends, f)

	// Add cards to friend
	c := &Card{ID: "card1", Col: Ready, Row: "friend"}
	c.Set(FieldTakenBy, "friend")
	c.Set(FieldProgress, stamp(now.Add(-30 * time.Minute)))
	s.Work.Table["friend"] = append(s.Work.Table["friend"], c)

	s.Now = now

	p, sent := TickRuleFriendIdle(s, TickReq{})
	require.Equal(t, 1, sent)
	require.NotEmpty(t, p.Notes)
	require.Contains(t, p.Notes[0].What, "idle-loaded")
}

func TestTickRuleFriendIdleReturn(t *testing.T) {
	t.Parallel()

	s := storetest.NewSnap(t)
	s.Work.SetRows(append(s.Work.Rows(), "friend"))

	now := time.Now()
	idleAfter := 20 * time.Minute

	// Friend with very stale evidence (> 2 idle bounds)
	f := FriendSeat{
		Name:   "friend",
		Width:  2,
		Status: Up,
		Active: now.Add(-60 * time.Minute),
	}
	s.Friends = append(s.Friends, f)

	// Add cards to friend
	c := &Card{ID: "card1", Col: Ready, Row: "friend"}
	c.Set(FieldTakenBy, "friend")
	c.Set(FieldProgress, stamp(now.Add(-60 * time.Minute)))
	s.Work.Table["friend"] = append(s.Work.Table["friend"], c)

	s.Now = now

	p, returned := TickRuleFriendIdleReturn(s, TickReq{})
	require.Greater(t, returned, 0)
	require.NotEmpty(t, p.Notes)
}

func TestIsIdleLoadedNoCards(t *testing.T) {
	t.Parallel()

	s := storetest.NewSnap(t)

	f := FriendSeat{
		Name:   "friend",
		Width:  2,
		Status: Up,
	}
	s.Friends = append(s.Friends, f)

	loaded, _ := IsIdleLoaded(s, f, 20*time.Minute)
	require.False(t, loaded)
}

func TestIsIdleLoadedNoEvidence(t *testing.T) {
	t.Parallel()

	s := storetest.NewSnap(t)
	s.Work.SetRows(append(s.Work.Rows(), "friend"))

	f := FriendSeat{
		Name:   "friend",
		Width:  2,
		Status: Up,
	}
	s.Friends = append(s.Friends, f)

	// Add card without evidence
	c := &Card{ID: "card1", Col: Ready, Row: "friend"}
	c.Set(FieldTakenBy, "friend")
	s.Work.Table["friend"] = append(s.Work.Table["friend"], c)

	loaded, _ := IsIdleLoaded(s, f, 20*time.Minute)
	require.True(t, loaded)
}

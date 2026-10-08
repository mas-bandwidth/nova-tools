package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuleFriendIdleLoaded(t *testing.T) {
	t.Parallel()
	// A friend with cards and stale evidence
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)), // stale evidence
		},
	})
	// Add a work card
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	assert.True(t, ruleFriendIdle(friends, row, s.Fleet, s.Now, 15*time.Minute))
}

func TestRuleFriendIdleNoCards(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	assert.False(t, ruleFriendIdle(friends, row, s.Fleet, s.Now, 15*time.Minute))
}

func TestRuleFriendIdleFreshEvidence(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-10 * time.Minute)), // fresh evidence
		},
	})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	assert.False(t, ruleFriendIdle(friends, row, s.Fleet, s.Now, 15*time.Minute))
}

func TestRuleFriendIdleNotUp(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "down",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Down}}
	assert.False(t, ruleFriendIdle(friends, row, s.Fleet, s.Now, 15*time.Minute))
}

func TestRuleFriendIdleNotFound(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "other", Status: Up}}
	assert.False(t, ruleFriendIdle(friends, row, s.Fleet, s.Now, 15*time.Minute))
}

func TestTickRuleIdleSentOnce(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	})
	s.Fleet.SetRows([]string{row})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	require.Greater(t, len(p.Notes), 0)
}

func TestTickRuleIdleFreshEvidence(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-5 * time.Minute)), // fresh evidence
		},
	})
	s.Fleet.SetRows([]string{row})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	require.Empty(t, p.Notes)
}

func TestTickRuleIdleTurnsOff(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	})
	s.Fleet.SetRows([]string{row})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: false})
	require.Empty(t, p.Notes)
}

func TestTickRuleIdleReturn(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  row,
		Row: row,
		Col: "up",
		Fields: map[string]string{
			"kind":       "work",
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	})
	s.Fleet.SetRows([]string{row})
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Ready,
		Fields: map[string]string{
			"kind":    "work",
			"primary": "test.1",
		},
	})
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	p, _ := TickRuleIdleReturn(s, TickReq{AnswerRules: true, Friends: friends})
	require.Greater(t, len(p.Units), 0)
}

func TestWidthGoalText(t *testing.T) {
	t.Parallel()
	text := WidthGoalText("freddy", 5, 3, 8, 25)
	assert.Contains(t, text, "freddy")
	assert.Contains(t, text, "5")
	assert.Contains(t, text, "3")
	assert.Contains(t, text, "8")
	assert.Contains(t, text, "25")
}

func TestEvidenceTime(t *testing.T) {
	t.Parallel()
	c := &Card{Fields: map[string]string{}}
	assert.True(t, evidenceTime(c).IsZero())
}

func TestEvidenceString(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC)
	s := evidenceString(now)
	assert.NotEmpty(t, s)
}

func TestFriendRow(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "friend.freddy", FriendRow("freddy"))
}

func TestFriendOfRow(t *testing.T) {
	t.Parallel()
	name, ok := FriendOfRow("friend.freddy")
	assert.True(t, ok)
	assert.Equal(t, "freddy", name)
}

func TestIsFriendRow(t *testing.T) {
	t.Parallel()
	assert.True(t, isFriendRow("friend.freddy"))
	assert.False(t, isFriendRow("machine1"))
}

func TestFindFriendSeat(t *testing.T) {
	t.Parallel()
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	seat := findFriendSeat(friends, "freddy")
	assert.NotNil(t, seat)
	assert.Equal(t, "freddy", seat.Name)
	seat = findFriendSeat(friends, "other")
	assert.Nil(t, seat)
}

func TestFriendLoadCounts(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Fleet: NewTable(Fleet)}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  "freddy.w1",
		Row: row,
		Col: Working,
	})
	s.Fleet.Put(&Card{
		ID:  "freddy.r1",
		Row: row,
		Col: Ready,
	})
	ready, working := friendLoadCounts(s, row)
	assert.Equal(t, 1, ready)
	assert.Equal(t, 1, working)
}

func TestTickRuleIdleWithNoFleet(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
	}
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	require.Empty(t, p.Notes)
}

package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuleFriendIdle(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("freddy")
	s.Fleet.Put(&Card{
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
			evidenceField: evidenceString(s.Now.Add(-10 * time.Minute)),
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Down,
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
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
	ctl := &Card{
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
			evidenceField: evidenceString(s.Now.Add(-30 * time.Minute)),
		},
	}
	s.Fleet.Put(ctl)
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

	// Simulate marking FieldFriendIdleLoaded
	ctl.Fields[FieldFriendIdleLoaded] = stamp(s.Now)

	// On next tick with no new work, it is sent once and not again
	p2, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	require.Empty(t, p2.Notes)
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
			evidenceField: evidenceString(s.Now.Add(-5 * time.Minute)),
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
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
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":                "member",
			"status":              Up,
			evidenceField:         evidenceString(s.Now.Add(-30 * time.Minute)),
			FieldFriendIdleLoaded: stamp(s.Now.Add(-16 * time.Minute)),
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
		Now:  time.Date(2026, 10, 7, 23, 11, 0, 0, time.UTC),
		Work: NewTable(Work),
	}
	friends := []FriendSeat{{Name: "freddy", Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	require.Empty(t, p.Notes)
}

func TestLoadedRowTurnsIdleLoadedAtBoundAndGoalMessageSentOnceWithLiveNumbers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:         now,
		Work:        NewTable(Work),
		Fleet:       NewTable(Fleet),
		Coordinator: "coordinator",
	}
	row := FriendRow("amy")
	ctl := &Card{
		ID:     CtlID(row),
		Row:    row,
		Col:    Ctl,
		Fields: map[string]string{"kind": "member", "status": Up},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})

	// 2 reads, 3 work cards
	s.Fleet.Put(&Card{ID: "amy.r1", Row: row, Col: Ready, Fields: map[string]string{"kind": "read", "taken": stamp(now.Add(-20 * time.Minute))}})
	s.Fleet.Put(&Card{ID: "amy.r2", Row: row, Col: Working, Fields: map[string]string{"kind": "read", "taken": stamp(now.Add(-20 * time.Minute))}})
	s.Fleet.Put(&Card{ID: "amy.w1", Row: row, Col: Ready, Fields: map[string]string{"kind": "work", "taken": stamp(now.Add(-20 * time.Minute))}})
	s.Fleet.Put(&Card{ID: "amy.w2", Row: row, Col: Working, Fields: map[string]string{"kind": "work", "taken": stamp(now.Add(-20 * time.Minute))}})
	s.Fleet.Put(&Card{ID: "amy.w3", Row: row, Col: Working, Fields: map[string]string{"kind": "work", "taken": stamp(now.Add(-20 * time.Minute))}})

	var goalSent bool
	var goalFriend string
	var goalReads, goalWork, goalWidth int
	var goalIdle int64
	sendWidthGoal := func(friend string, reads, work, width int, idle int64) error {
		goalSent = true
		goalFriend = friend
		goalReads = reads
		goalWork = work
		goalWidth = width
		goalIdle = idle
		return nil
	}

	friends := []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends, SendWidthGoal: sendWidthGoal})

	require.Len(t, p.Notes, 1)
	assert.Equal(t, Happened, p.Notes[0].Kind)
	assert.Equal(t, "inbox", p.Notes[0].Type)
	assert.Equal(t, "friend amy idle-loaded 20m: width goal sent", p.Notes[0].What)
	assert.Equal(t, "coordinator", p.Notes[0].To)

	assert.True(t, goalSent)
	assert.Equal(t, "amy", goalFriend)
	assert.Equal(t, 2, goalReads)
	assert.Equal(t, 3, goalWork)
	assert.Equal(t, 8, goalWidth)
	assert.Equal(t, int64(20), goalIdle)

	require.Len(t, p.Units, 1)
	assert.Equal(t, ctl.ID, p.Units[0].Key)
	ctl.Fields[FieldFriendIdleLoaded] = stamp(now)

	// Second tick: sent once!
	goalSent = false
	p2, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends, SendWidthGoal: sendWidthGoal})
	assert.Empty(t, p2.Notes)
	assert.False(t, goalSent)
}

func TestProgressLineResetsIdleLoaded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("amy")
	ctl := &Card{
		ID:     CtlID(row),
		Row:    row,
		Col:    Ctl,
		Fields: map[string]string{"kind": "member", "status": Up, FieldFriendIdleLoaded: stamp(now.Add(-10 * time.Minute))},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})

	wc := &Card{
		ID:     "amy.w1",
		Row:    row,
		Col:    Working,
		Fields: map[string]string{"kind": "work", FieldProgress: stamp(now.Add(-2 * time.Minute))},
	}
	s.Fleet.Put(wc)

	friends := []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})

	// Evidence is fresh (2m old <= 15m bound), so idle-loaded is reset!
	assert.Empty(t, p.Notes)
	require.Len(t, p.Units, 1)
	assert.Equal(t, ctl.ID, p.Units[0].Key)
	assert.Equal(t, "friend amy evidence fresh: idle-loaded reset", p.Units[0].Moved)
}

func TestBeatWithChildrenResetsIdleLoaded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("amy")
	ctl := &Card{
		ID:     CtlID(row),
		Row:    row,
		Col:    Ctl,
		Fields: map[string]string{"kind": "member", "status": Up, FieldFriendIdleLoaded: stamp(now.Add(-10 * time.Minute))},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})

	wc := &Card{
		ID:     "amy.w1",
		Row:    row,
		Col:    Working,
		Fields: map[string]string{"kind": "work", "taken": stamp(now.Add(-30 * time.Minute))},
	}
	s.Fleet.Put(wc)

	workingChildren := 2
	beats := map[string]Beat{
		"amy": {
			At: now.Add(-1 * time.Minute),
			Friend: &FriendReport{
				Working: &workingChildren,
			},
		},
	}

	friends := []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends, Beats: beats})

	// Beat reporting children is evidence and fresh, so idle-loaded is reset!
	assert.Empty(t, p.Notes)
	require.Len(t, p.Units, 1)
	assert.Equal(t, ctl.ID, p.Units[0].Key)
	assert.Equal(t, "friend amy evidence fresh: idle-loaded reset", p.Units[0].Moved)
}

func TestOneBoundLaterCardsReturnToPoolAndRowMarkedIdleWithOneJudgment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("amy")
	ctl := &Card{
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":                "member",
			"status":              Up,
			FieldFriendIdleLoaded: stamp(now.Add(-16 * time.Minute)),
		},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})

	// Add work card and read card with primaries
	s.Work.Put(&Card{ID: "task1", Row: "stream1", Col: Working, Fields: map[string]string{"work": "amy.w1"}})
	s.Work.Put(&Card{ID: "task2", Row: "stream1", Col: Review, Fields: map[string]string{}})

	wc := &Card{
		ID:     "amy.w1",
		Row:    row,
		Col:    Working,
		Fields: map[string]string{"kind": "work", "primary": "task1", "taken": stamp(now.Add(-35 * time.Minute))},
	}
	rc := &Card{
		ID:     "amy.r1",
		Row:    row,
		Col:    Ready,
		Fields: map[string]string{"kind": "read", "primary": "task2", "taken": stamp(now.Add(-35 * time.Minute))},
	}
	s.Fleet.Put(wc)
	s.Fleet.Put(rc)

	friends := []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p, _ := TickRuleIdleReturn(s, TickReq{AnswerRules: true, Friends: friends})

	// 1 judgment for the row in seat's inbox
	require.Len(t, p.Notes, 1)
	assert.Equal(t, Judgment, p.Notes[0].Kind)
	assert.Equal(t, RuleFriendIdleReturn, p.Notes[0].Type)
	assert.Equal(t, row, p.Notes[0].Stream)
	assert.True(t, p.Notes[0].StreamLevel)
	assert.Contains(t, p.Notes[0].What, "friend amy idle: cards returned to pool")

	// Cards returned and row marked idle
	require.GreaterOrEqual(t, len(p.Units), 3)

	// Check work card unit
	var workUnit *Unit
	var rowUnit *Unit
	for i := range p.Units {
		if p.Units[i].Key == "amy.w1" {
			workUnit = &p.Units[i]
		}
		if p.Units[i].Key == ctl.ID {
			rowUnit = &p.Units[i]
		}
	}
	require.NotNil(t, workUnit)
	assert.Contains(t, workUnit.Moved, "amy.w1 returned to pool by rule friend-idle-return; task1 working -> ready")

	require.NotNil(t, rowUnit)
	assert.Contains(t, rowUnit.Moved, "friend amy row marked idle")
}

func TestSettingChangesBound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	s.Work.SetProp(PropFriendIdle, "5m")

	row := FriendRow("amy")
	ctl := &Card{
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":        "member",
			"status":      Up,
			evidenceField: evidenceString(now.Add(-6 * time.Minute)),
		},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})
	s.Fleet.Put(&Card{
		ID:     "amy.w1",
		Row:    row,
		Col:    Working,
		Fields: map[string]string{"kind": "work", "primary": "task1"},
	})

	friends := []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	assert.NotEmpty(t, p.Notes, "at 6m idle with 5m bound, should be idle-loaded")

	// At 4m idle with 5m bound
	ctl.Fields[evidenceField] = evidenceString(now.Add(-4 * time.Minute))
	p2, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends})
	assert.Empty(t, p2.Notes, "at 4m idle with 5m bound, should not be idle-loaded")
}

func TestTakeCardClearsIdle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	row := FriendRow("amy")
	ctl := &Card{
		ID:  CtlID(row),
		Row: row,
		Col: Ctl,
		Fields: map[string]string{
			"kind":                "member",
			"status":              "idle",
			FieldFriendIdleSince:  stamp(now.Add(-10 * time.Minute)),
			FieldFriendIdleReason: "idle-loaded for two bounds",
			FieldFriendIdleLoaded: stamp(now.Add(-25 * time.Minute)),
		},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})

	wc := &Card{
		ID:     "amy.w1",
		Row:    row,
		Col:    Ready,
		Fields: map[string]string{"kind": "work", "primary": "task1", "gen": "1"},
	}
	s.Fleet.Put(wc)

	s.Friends = []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p := Take(s, TakeReq{Sel: Sel{IDs: []string{wc.ID}}, As: row, Gens: map[string]int{wc.ID: 1}, Who: row})

	require.Len(t, p.Units, 2)
	assert.Equal(t, wc.ID, p.Units[0].Key)
	assert.Equal(t, ctl.ID, p.Units[1].Key)
	assert.Contains(t, p.Units[1].Moved, "idle cleared")
}


func TestStaleBeatWithChildrenDoesNotSuppressIdleLoaded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:         now,
		Work:        NewTable(Work),
		Fleet:       NewTable(Fleet),
		Coordinator: "coordinator",
	}
	row := FriendRow("amy")
	ctl := &Card{
		ID:     CtlID(row),
		Row:    row,
		Col:    Ctl,
		Fields: map[string]string{"kind": "member", "status": Up},
	}
	s.Fleet.Put(ctl)
	s.Fleet.SetRows([]string{row})

	wc := &Card{
		ID:     "amy.w1",
		Row:    row,
		Col:    Working,
		Fields: map[string]string{"kind": "work", "taken": stamp(now.Add(-30 * time.Minute))},
	}
	s.Fleet.Put(wc)

	workingChildren := 2
	beats := map[string]Beat{
		"amy": {
			At: now.Add(-20 * time.Minute),
			Friend: &FriendReport{
				Working: &workingChildren,
			},
		},
	}

	friends := []FriendSeat{{Name: "amy", Width: 8, Status: Up}}
	p, _ := TickRuleIdle(s, TickReq{AnswerRules: true, Friends: friends, Beats: beats})

	// Stale beat (20m > 15m default bound) does not count as evidence: friend is idle-loaded!
	require.Len(t, p.Notes, 1)
	assert.Equal(t, Happened, p.Notes[0].Kind)
	assert.Equal(t, "inbox", p.Notes[0].Type)
	assert.Equal(t, "friend amy idle-loaded 30m: width goal sent", p.Notes[0].What)
	assert.Equal(t, "coordinator", p.Notes[0].To)
}

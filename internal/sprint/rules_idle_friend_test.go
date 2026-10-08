package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// TestFriendIdleRule: pure, fake clock, no sockets, t.Parallel, testify.
// A loaded row with stale evidence turns idle-loaded at the bound and the goal message is sent once.
func TestFriendIdleRule(t *testing.T) {
	t.Parallel()

	// Set up test with friend holding cards but no recent work
	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "15m",
			},
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence: "up",
					},
				},
			},
			Cards: []*Card{
				// Friend holds a work card
				{
					ID: "work-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "work",
						"col": "working",
						FieldProgress: stamp(time.Now().Add(-2 * time.Hour)), // stale evidence
					},
				},
				// Friend holds a read card
				{
					ID: "read-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "read",
						"col": "ready",
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(time.Now()),
		Friends: s.Fleet.Friends,
	}

	// Should detect idle-loaded
	idle, mins := idleLoad(s, r.Friends[0])
	if !idle {
		t.Error("expected friend alice to be idle-loaded")
	}
	if mins < 110 {
		t.Errorf("expected idle mins >= 110, got %d", mins)
	}
}

// TestFriendIdleNotIdle: loaded friend with recent evidence is not idle-loaded.
func TestFriendIdleNotIdle(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "15m",
			},
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence: "up",
					},
				},
			},
			Cards: []*Card{
				{
					ID: "work-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "work",
						"col": "working",
						FieldProgress: stamp(time.Now().Add(-10 * time.Minute)), // fresh evidence
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(time.Now()),
		Friends: s.Fleet.Friends,
	}

	idle, _ := idleLoad(s, r.Friends[0])
	if idle {
		t.Error("expected friend alice not to be idle-loaded with recent evidence")
	}
}

// TestFriendIdleNotLoaded: friend with no taken cards is not idle-loaded.
func TestFriendIdleNotLoaded(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Work: &Work{
			Width: 3,
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence: "up",
					},
				},
			},
			// No cards for this friend
		},
	}
	r := TickReq{
		Clock: fakeClock(time.Now()),
		Friends: s.Fleet.Friends,
	}

	idle, _ := idleLoad(s, r.Friends[0])
	if idle {
		t.Error("expected friend with no cards not to be idle-loaded")
	}
}

// TestFriendIdleDown: friend with presence down is not idle-loaded.
func TestFriendIdleDown(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Work: &Work{
			Width: 3,
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence: "down",
					},
				},
			},
			Cards: []*Card{
				{
					ID: "work-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "work",
						"col": "working",
						FieldProgress: stamp(time.Now().Add(-2 * time.Hour)),
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(time.Now()),
		Friends: s.Fleet.Friends,
	}

	idle, _ := idleLoad(s, r.Friends[0])
	if idle {
		t.Error("expected friend with presence down not to be idle-loaded")
	}
}

// TestFriendIdleMessageSentOnce: goal message sent only once per idle-loaded event.
func TestFriendIdleMessageSentOnce(t *testing.T) {
	t.Parallel()

	elapsed time.Now()
	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "15m",
			},
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence:  "up",
						FieldIdleLoaded: "1",
						FieldIdleLoadedAt: stamp(elapsed.Add(-30 * time.Minute)),
					},
				},
			},
			Cards: []*Card{
				{
					ID: "work-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "work",
						"col": "working",
						FieldProgress: stamp(elapsed.Add(-2 * time.Hour)),
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(elapsed),
		Friends: s.Fleet.Friends,
	}

	plan, msg := ruleFriendIdle(s, r, r.Friends[0], 120)
	if msg != nil {
		t.Error("expected no message when already sent this tick")
	}
	if len(plan.Changes) > 0 {
		t.Error("expected no changes when already sent this tick")
	}
}

// TestFriendIdleReturn: after another idle bound, cards returned to pool.
func TestFriendIdleReturn(t *testing.T) {
	t.Parallel()

	elapsed time.Now()
	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "15m",
			},
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence:     "up",
						FieldIdleLoaded:     "1",
						FieldIdleLoadedAt: stamp(elapsed.Add(-30 * time.Minute)),
					},
				},
			},
			Cards: []*Card{
				{
					ID: "work-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "work",
						"col": "working",
					},
				},
				{
					ID: "read-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "read",
						"col": "ready",
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(elapsed),
		Friends: s.Fleet.Friends,
	}

	plan := ruleFriendIdleReturn(s, r, r.Friends[0], 120)
	if len(plan.Changes) < 1 {
		t.Error("expected changes when returning cards")
	}
	if len(plan.Notes) < 1 {
		t.Error("expected judgment note when returning cards")
	}
}

// TestFriendIdleReturnNotReady: not yet one idle bound later.
func TestFriendIdleReturnNotReady(t *testing.T) {
	t.Parallel()

	elapsed time.Now()
	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "15m",
			},
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence:     "up",
						FieldIdleLoaded:     "1",
						FieldIdleLoadedAt: stamp(elapsed.Add(-10 * time.Minute)),
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(elapsed),
		Friends: s.Fleet.Friends,
	}

	plan := ruleFriendIdleReturn(s, r, r.Friends[0], 10)
	if len(plan.Changes) > 0 {
		t.Error("expected no changes when not yet one idle bound later")
	}
}

// TestComposeWidthGoal: width goal message composition.
func TestComposeWidthGoal(t *testing.T) {
	t.Parallel()

	msg := composeWidthGoal("alice", 3, 2, 1, 45)
	if !strings.Contains(msg, "alice") {
		t.Error("expected message to contain friend name")
	}
	if !strings.Contains(msg, "width") {
		t.Error("expected message to contain width")
	}
}

// TestFriendIdleSetting: friend-idle setting changes the bound.
func TestFriendIdleSetting(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "5m",
			},
		},
	}

	if dur := s.FriendIdleAfter(); dur != 5*time.Minute {
		t.Errorf("expected 5m, got %s", dur)
	}
}

// TestFriendIdleWithBeatChildren: friend with beat children is not idle-loaded.
func TestFriendIdleWithBeatChildren(t *testing.T) {
	t.Parallel()

	elapsed time.Now()
	s := &Snapshot{
		Work: &Work{
			Width: 3,
			Props: map[string]string{
				PropFriendIdle: "15m",
			},
		},
		Fleet: &Fleet{
			Friends: []FriendSeat{{
				Name: "alice",
				FriendRow: "friend-alice",
				BeatChildren: "work-1",
				F: map[string]string{
					"beat_time": stamp(elapsed.Add(-5 * time.Minute)),
				},
			}},
			Rows: map[string]*Row{
				"friend-alice": {
					ID: "friend-alice",
					F: map[string]string{
						FieldPresence: "up",
					},
				},
			},
			Cards: []*Card{
				{
					ID: "work-1",
					F: map[string]string{
						"row": "friend-alice",
						"kind": "work",
						"col": "working",
						FieldProgress: stamp(elapsed.Add(-2 * time.Hour)), // stale
					},
				},
			},
		},
	}
	r := TickReq{
		Clock: fakeClock(elapsed),
		Friends: s.Fleet.Friends,
	}

	idle, _ := idleLoad(s, r.Friends[0])
	if idle {
		t.Error("expected friend with beat children not to be idle-loaded")
	}
}

package sprint

import (
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The rules that detect and handle idle-loaded friends.
const (
	RuleFriendIdle    = "friend-idle"     // friend row is loaded but idle: sent width goal
	RuleFriendIdleReturn = "friend-idle-return" // still idle: cards returned to pool
)

// The fields these rules write.
const (
	// FieldIdleLoaded is the stamp on a friend's row when she becomes idle-loaded.
	FieldIdleLoaded = "idle_loaded"
	// FieldIdleLoadedAt is when she became idle-loaded (for timing the second rule).
	FieldIdleLoadedAt = "idle_loaded_at"
	// FieldIdleReason is why the row is marked idle (not down).
	FieldIdleReason = "idle_reason"
	// FieldIdleSince is when the row was marked idle.
	FieldIdleSince = "idle_since"
)

// The tick parts that apply these rules.
const (
	PartFriendIdle    = "friend-idle"
	PartFriendIdleReturn = "friend-idle-return"
)

// idleLoad: a friend row with at least one taken card (working or ready) whose newest
// evidence of work is older than the idle bound (friendIdleAfter), while her presence is up.
// Returns true if the friend is idle-loaded, with the minutes idle.
func idleLoad(s *Snapshot, friend FriendSeat) (bool, int) {
	if friend.FriendRow == "" {
		return false, 0
	}
	friends := s.Fleet.Row(friend.FriendRow)
	if friends == nil || friends.F(FieldPresence) != "up" {
		return false, 0
	}
	// Count taken cards and find newest evidence of work
	takenCards := 0
	var newestEvidence time.Time
	for _, wc := range s.Fleet.Cards() {
		if !wc.Placed() || wc.F("row") != friend.FriendRow {
			continue
		}
		if wc.Col != Ready && wc.Col != Working {
			continue
		}
		takenCards++
		// Check for evidence: progress, finish, read verdict, lane start
		if progress := wc.F(FieldProgress); progress != "" {
			if t, err := parseStamp(progress); err == nil && t.After(newestEvidence) {
				newestEvidence = t
			}
		}
		if done := wc.F(FieldDone); done != "" {
			if t, err := parseStamp(done); err == nil && t.After(newestEvidence) {
				newestEvidence = t
			}
		}
	}
	if takenCards == 0 {
		return false, 0
	}
	// Check if there is any recent finish on this friend's cards
	for _, wc := range s.Fleet.Cards() {
		if !wc.Placed() || wc.F("row") != friend.FriendRow {
			continue
		}
		if done := wc.F(FieldDone); done != "" {
			if t, err := parseStamp(done); err == nil && t.After(newestEvidence) {
				newestEvidence = t
			}
		}
	}
	// Check beat for children (friendStarted)
	if friendsStarted(s, friend) {
		return false, 0
	}
	// Determine idle time
	if newestEvidence.IsZero() {
		return false, 0
	}
	elapsed := r.Clock.Now().Sub(newestEvidence)
	if elapsed < s.FriendIdleAfter() {
		return false, 0
	}
	return true, int(elapsed.Minutes())
}

// friendsStarted says the beat has children on cards of this friend.
func friendsStarted(s *Snapshot, friend FriendSeat) bool {
	if friend.BeatChildren == "" {
		return false
	}
	beatTime, _ := parseStamp(friend.F("beat_time"))
	if beatTime.IsZero() {
		return false
	}
	// Check if any child card has recent progress or is in progress
	now := r.Clock.Now()
	for _, wc := range s.Fleet.Cards() {
		if !wc.Placed() || wc.F("row") != friend.FriendRow {
			continue
		}
		if progress := wc.F(FieldProgress); progress != "" {
			if t, err := parseStamp(progress); err == nil && t.After(beatTime) && now.Sub(t) < s.FriendStallAfter() {
				return true
			}
		}
	}
	return false
}

// ruleFriendIdle: first time a row turns idle-loaded, send one bus message with the width
// goal and write a judgment-free line to the seat's inbox feed.
func ruleFriendIdle(s *Snapshot, r TickReq, friend FriendSeat, idleMins int) (Plan, *bus.Message) {
	if friend.FriendRow == "" {
		return Plan{}, nil
	}
	friends := s.Fleet.Row(friend.FriendRow)
	if friends == nil || friends.F(FieldPresence) != "up" {
		return Plan{}, nil
	}
	// Check if already sent this tick
	if friends.F(FieldIdleLoaded) != "" && friends.F(FieldIdleLoadedAt) != "" {
		return Plan{}, nil
	}
	// Gather numbers
	takenReads := 0
	takenWork := 0
	for _, wc := range s.Fleet.Cards() {
		if !wc.Placed() || wc.F("row") != friend.FriendRow {
			continue
		}
		if wc.F("kind") == "read" {
			takenReads++
		} else if wc.F("kind") == "work" {
			takenWork++
		}
	}
	width := s.Work.Width()
	// Compose width goal message
	widthGoal := composeWidthGoal(friend.Name, width, takenReads, takenWork, idleMins)
	msg := bus.Message{
		Subject: "WIDTH: your row is loaded and idle",
		Body:    widthGoal,
	}
	// Write to inbox feed
	plan := Plan{
		Changes: []Change{
			{
				ID:    friend.FriendRow,
				Table: "friends",
				Set: map[string]string{
					FieldIdleLoaded: "1",
					FieldIdleLoadedAt: stamp(r.Clock.Now()),
				},
			},
		},
		Notes: []Note{
			{
				Kind:     Note,
				To:       "seat",
				Subject:  "inbox",
				Body:     fmt.Sprintf("friend %s idle-loaded %dm: width goal sent", friend.Name, idleMins),
			},
		},
	}
	return plan, &msg
}

// composeWidthGoal: a paragraph explaining the width goal, parameterized by name and width.
func composeWidthGoal(name string, width int, takenReads, takenWork, idleMins int) string {
	return fmt.Sprintf("Friend %s: your width is %d, but you are holding %d read cards and %d work cards in working or ready, with no progress for %dm. The goal is to take one card at a time to width %d, report on it, and then take another. Let the machine give you one card; do not take more.", name, width, takenReads, takenWork, idleMins, width)
}

// ruleFriendIdleReturn: if still idle-loaded one idle bound later, return cards to pool, mark row idle.
func ruleFriendIdleReturn(s *Snapshot, r TickReq, friend FriendSeat, idleMins int) Plan {
	if friend.FriendRow == "" {
		return Plan{}
	}
	friends := s.Fleet.Row(friend.FriendRow)
	if friends == nil {
		return Plan{}
	}
	// Check if already returned this tick
	if friends.F(FieldIdleReason) != "" && friends.F(FieldIdleSince) != "" {
		return Plan{}
	}
	// Check time since idle-loaded
	loadedAtStr := friends.F(FieldIdleLoadedAt)
	if loadedAtStr == "" {
		return Plan{}
	}
	loadedAt, _ := parseStamp(loadedAtStr)
	if loadedAt.IsZero() {
		return Plan{}
	}
	elapsed := r.Clock.Now().Sub(loadedAt)
	if elapsed < s.FriendIdleAfter() {
		return Plan{}
	}
	// Return cards to pool
	plan := Plan{
		Changes: []Change{
			{
				ID:    friend.FriendRow,
				Table: "friends",
				Set: map[string]string{
					FieldIdleReason: "idle-loaded",
					FieldIdleSince:  stamp(r.Clock.Now()),
				},
			},
		},
	}
	// Find and return cards
	for _, wc := range s.Fleet.Cards() {
		if !wc.Placed() || wc.F("row") != friend.FriendRow {
			continue
		}
		if wc.F("kind") == "read" {
			// Return reads to review
			plan.AddChange(Change{
				ID:    wc.ID,
				Table: "work",
				Set: map[string]string{
					"kind": "read",
					"col":  "review",
					"reason": "returned to pool: friend " + friend.Name + " idle-loaded",
				},
			})
		} else if wc.F("kind") == "work" {
			// Return work to ready
			plan.AddChange(Change{
				ID:    wc.ID,
				Table: "work",
				Set: map[string]string{
					"col":  "ready",
					"reason": "returned to pool: friend " + friend.Name + " idle-loaded",
				},
			})
		}
	}
	// Add judgment note
	plan.Notes = append(plan.Notes, Note{
		Kind:     Judgment,
		Type:     "NIdle",
		To:       "seat",
		Subject:  "friend " + friend.Name,
		Body:     fmt.Sprintf("friend %s idle-loaded %dm: cards returned to pool", friend.Name, idleMins),
		Decision: "ack",
	})
	return plan
}

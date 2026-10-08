package sprint

import (
	"fmt"
	"time"
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

// idleLoad says a friend is idle-loaded (has taken cards, status is up,
// and newest evidence of work is older than the idle bound).
func idleLoad(s *Snapshot, friend FriendSeat) (bool, int) {
	if friend.Name == "" {
		return false, 0
	}
	// Check if friend has taken cards and is up
	if friend.Status != Up {
		return false, 0
	}
	// Count taken cards and find newest evidence of work
	takenCards := 0
	var newestEvidence time.Time
	for _, wc := range s.Work.Cards() {
		if !wc.Placed() || wc.F("row") != FriendRow(friend.Name) {
			continue
		}
		if wc.Col != Ready && wc.Col != Working {
			continue
		}
		takenCards++
		// Check for evidence: progress
		if progress := wc.F(FieldProgress); progress != "" {
			if t, ok := tryParseStamp(progress); ok && t.After(newestEvidence) {
				newestEvidence = t
			}
		}
	}
	if takenCards == 0 {
		return false, 0
	}
	// Check if beat has children (friendStarted)
	if len(friend.Running) > 0 {
		return false, 0
	}
	// Determine idle time
	if newestEvidence.IsZero() {
		return false, 0
	}
	elapsed := s.Now.Sub(newestEvidence)
	if elapsed < s.FriendIdleAfter() {
		return false, 0
	}
	return true, int(elapsed.Minutes())
}

// tryParseStamp attempts to parse a stamp string into a time.
func tryParseStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	// Try RFC3339 format
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	// Try other formats
	for _, format := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(format, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ruleFriendIdle: first time a row turns idle-loaded, sends width goal.
// Returns true if a rule answer was produced.
func ruleFriendIdle(s *Snapshot, friend FriendSeat) (friendIdle bool, mins int) {
	idle, mins := idleLoad(s, friend)
	return idle, mins
}

// ruleFriendIdleReturn: if still idle-loaded one idle bound later, returns cards.
// Returns true if cards were returned.
func ruleFriendIdleReturn(s *Snapshot, friend FriendSeat, idleMins int) bool {
	// Check friend row in Fleet table
	friends := s.Fleet.Cards()
	// Find friend's row
	var friendRow *Card
	for _, c := range friends {
		if c.F("kind") == "friend" && c.F("name") == friend.Name {
			friendRow = c
			break
		}
	}
	if friendRow == nil {
		return false
	}
	// Check if already returned
	if friendRow.F(FieldIdleReason) != "" {
		return false
	}
	// Check time since idle-loaded
	loadedAtStr := friendRow.F(FieldIdleLoadedAt)
	if loadedAtStr == "" {
		return false
	}
	loadedAt, ok := tryParseStamp(loadedAtStr)
	if !ok || loadedAt.IsZero() {
		return false
	}
	elapsed := s.Now.Sub(loadedAt)
	if elapsed < s.FriendIdleAfter() {
		return false
	}
	return true
}

// composeWidthGoal: a paragraph explaining the width goal.
func composeWidthGoal(name string, width, takenReads, takenWork, idleMins int) string {
	return fmt.Sprintf("Friend %s: your width is %d, but you are holding %d read cards and %d work cards in working or ready, with no progress for %dm. The goal is to take one card at a time to width %d, report on it, and then take another. Let the machine give you one card; do not take more.", name, width, takenReads, takenWork, idleMins, width)
}

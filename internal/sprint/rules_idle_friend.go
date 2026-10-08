package sprint

import (
	"fmt"
	"time"
)

// friendIdleBoundDefault is the default duration for detecting an idle-loaded friend.
const friendIdleBoundDefault = 15 * time.Minute

// evidenceField is the field that holds the latest evidence of work for a friend's row.
const evidenceField = "evidence"

// EvidenceField names the field that holds the latest evidence of work for a friend's row.
const EvidenceField = evidenceField

// evidenceTime returns the time of the latest evidence of work.
func evidenceTime(c *Card) time.Time {
	v := c.F(evidenceField)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

// evidenceString returns the evidence value as it should be stored.
func evidenceString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// isFriendRow says the row is a friend's row.
func isFriendRow(row string) bool {
	return len(row) > 7 && row[:7] == "friend."
}

// friendIdleBound returns the friend-idle setting bound, or the default.
func (s *Snapshot) friendIdleBound() time.Duration {
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropFriendIdle); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return friendIdleBoundDefault
}

// ruleFriendIdle computes whether a friend row should trigger the friend-idle rule.
func ruleFriendIdle(friends []FriendSeat, row string, fleet *Table, now time.Time, bound time.Duration) bool {
	if !isFriendRow(row) {
		return false
	}
	// Check if friend is up (presence)
	friendName, _ := FriendOfRow(row)
	friendSeat := findFriendSeat(friends, friendName)
	if friendSeat == nil {
		return false
	}
	if friendSeat.Status != Up {
		return false
	}
	// Check if friend has cards
	ready, working := 0, 0
	if fleet != nil {
		ready = fleet.Count(row, Ready)
		working = fleet.Count(row, Working)
	}
	if ready+working == 0 {
		return false
	}
	// Check if evidence is older than the bound
	var card *Card
	if fleet != nil {
		card = fleet.Card(row)
	}
	if card == nil {
		return false
	}
	evidence := evidenceTime(card)
	if evidence.IsZero() {
		return false
	}
	if now.Sub(evidence) <= bound {
		return false
	}
	return true
}

// WidthGoalText returns the width goal message text.
func WidthGoalText(friendName string, takenReads, takenWork, width int, idleMinutes int64) string {
	return fmt.Sprintf("Width goal for %s: you are holding %d reads and %d work cards (width %d). You have been idle for %d minutes. Please focus on your assigned tasks.",
		friendName, takenReads, takenWork, width, idleMinutes)
}

// BusMessageSubjectIdleLoaded returns the subject for the width goal bus message.
const BusMessageSubjectIdleLoaded = "WIDTH: your row is loaded and idle"

// FieldFriendIdleSince is the field written on a friend's row when it is marked idle.
const FieldFriendIdleSince = "friend_idle_since"

// FieldFriendIdleReason is the reason a friend's row is marked idle.
const FieldFriendIdleReason = "friend_idle_reason"

// findFriendSeat finds the friend seat by name.
func findFriendSeat(friends []FriendSeat, name string) *FriendSeat {
	for i := range friends {
		if friends[i].Name == name {
			return &friends[i]
		}
	}
	return nil
}

// friendLoadCounts returns the ready and working counts on a friend's row.
func friendLoadCounts(s *Snapshot, row string) (ready, working int) {
	if s == nil || s.Fleet == nil {
		return 0, 0
	}
	ready = s.Fleet.Count(row, Ready)
	working = s.Fleet.Count(row, Working)
	return ready, working
}

// TickRuleIdle sends width goal to friends who are loaded but idle.
func TickRuleIdle(s *Snapshot, r TickReq) (Plan, int) {
	if !r.AnswerRules {
		return Plan{}, 0
	}
	var notes []Note
	bound := s.friendIdleBound()
	for _, row := range s.Fleet.Rows() {
		if !isFriendRow(row) {
			continue
		}
		if ruleFriendIdle(r.Friends, row, s.Fleet, s.Now, bound) {
			friend, _ := FriendOfRow(row)
			ready, working := friendLoadCounts(s, row)
			card := s.Fleet.Card(row)
			var idleMinutes int64
			if card != nil {
				evidence := evidenceTime(card)
				if !evidence.IsZero() {
					idleMinutes = int64(s.Now.Sub(evidence).Minutes())
				}
			}
			// Bus message to nova-bus2
			notes = append(notes, Note{
				Kind:      Happened,
				Type:      "friend loaded but idle",
				Stream:    row,
				What:      WidthGoalText(friend, ready, working, 0, idleMinutes),
				Who:       "rule " + RuleFriendIdle,
				At:        s.Now,
				To:        "coordinator",
			})
			// Judgment-free line to seat's inbox feed
			notes = append(notes, Note{
				Kind:      Happened,
				Type:      "inbox",
				Stream:    row,
				What:      fmt.Sprintf("friend %s idle-loaded %dm: width goal sent", friend, idleMinutes),
				Who:       "rule " + RuleFriendIdle,
				At:        s.Now,
			})
		}
	}
	return Plan{Notes: notes}, 0
}

// TickRuleIdleReturn returns cards to pool when friend remains idle for a second bound.
func TickRuleIdleReturn(s *Snapshot, r TickReq) (Plan, int) {
	if !r.AnswerRules {
		return Plan{}, 0
	}
	var p Plan
	bound := s.friendIdleBound()
	for _, row := range s.Fleet.Rows() {
		if !isFriendRow(row) {
			continue
		}
		// Check if friend is still idle (evidence still old)
		if !ruleFriendIdle(r.Friends, row, s.Fleet, s.Now, bound) {
			continue
		}
		// Return all ready cards to review (reads to review, work to ready)
		for _, c := range s.Fleet.Cell(row, Ready) {
			p.Units = append(p.Units, Unit{
				Key:    c.ID,
				Stream: row,
				Changes: []Change{change(Fleet, setEntry(c, map[string]string{
					FieldTakenBack: RuleFriendIdleReturn + " at " + stamp(s.Now),
					"reason":       "returned to pool by friend-idle-return",
				}))},
				Moved:  c.ID + " returned to pool by rule " + RuleFriendIdleReturn,
			})
		}
		// Return all working cards to ready
		for _, c := range s.Fleet.Cell(row, Working) {
			p.Units = append(p.Units, Unit{
				Key:    c.ID,
				Stream: row,
				Changes: []Change{change(Fleet, setEntry(c, map[string]string{
					FieldTakenBack: RuleFriendIdleReturn + " at " + stamp(s.Now),
					"reason":       "returned to pool by friend-idle-return",
				}))},
				Moved:  c.ID + " returned to pool by rule " + RuleFriendIdleReturn,
			})
		}
		// Mark the row idle
		p.Units = append(p.Units, Unit{
			Key:    row,
			Stream: row,
			Changes: []Change{change(Fleet, setEntry(s.Fleet.Card(row), map[string]string{
				FieldFriendIdleSince:  stamp(s.Now),
				FieldFriendIdleReason: "idle-loaded for two bounds",
			}))},
		})
	}
	return p, 0
}

package sprint

import (
	"fmt"
	"time"
)

// The friend-idle and friend-idle-return rules (docs/SPEC-SPRINT.md section 8, answered by rule):
// a friend who is loaded but idle is detected by the machine and sent the width goal, then
// relieved of the cards; the seat looks at nothing. The model is tla/SprintRules.tla
// (IdleFriend, IdleReturn).

const (
	// RuleFriendIdle is the rule that detects a loaded friend with stale work evidence and
	// sends her the width goal message (nova-bus2, subject WIDTH).
	RuleFriendIdle = "friend-idle"
	// RuleFriendIdleReturn is the rule that returns a still-idle friend's cards to the pool
	// and marks her row idle.
	RuleFriendIdleReturn = "friend-idle-return"
)

// IsIdleLoaded says the friend is loaded but idle: she has at least one taken card
// (working or ready for her) and her newest evidence of work is older than the idle bound.
// The returned time is when the evidence was seen (zero if none).
func IsIdleLoaded(s *Snapshot, f FriendSeat, idleAfter time.Duration) (bool, time.Time) {
	// Find all cards taken by this friend (ready or working for her)
	cards := friendLoadedCards(s, f.Name)
	if len(cards) == 0 {
		return false, time.Time{}
	}

	// Find the newest evidence of work among these cards
	var newestEvidence time.Time
	for _, c := range cards {
		// Evidence: progress line
		if p := c.F(FieldProgress); p != "" {
			if t, ok := tryParseStamp(p); ok && t.After(newestEvidence) {
				newestEvidence = t
			}
		}
		// Evidence: friend's activity timestamp
		if !f.Active.IsZero() && f.Active.After(newestEvidence) {
			newestEvidence = f.Active
		}
	}

	if newestEvidence.IsZero() {
		return true, time.Time{}
	}

	if s.Now.Sub(newestEvidence) > idleAfter {
		return true, newestEvidence
	}
	return false, newestEvidence
}

// friendLoadedCards returns the work cards taken by the friend (ready or working for her).
func friendLoadedCards(s *Snapshot, friend string) []*Card {
	var cards []*Card
	for _, row := range s.Work.Rows() {
		if row != friend {
			continue
		}
		for _, c := range s.Work.Table[row] {
			if isRead(c) {
				continue
			}
			// Check if card is working or ready for this friend
			if c.Col == Working || c.Col == Ready {
				cards = append(cards, c)
			}
		}
	}
	return cards
}

// TickRuleFriendIdle detects loaded idle friends and sends them the width goal message.
// It returns true if it sent any messages.
func TickRuleFriendIdle(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	sent := 0

	idleAfter := s.FriendIdleAfter()

	for _, f := range s.Friends {
		if f.Status != Up {
			continue
		}

		loaded, _ := IsIdleLoaded(s, f, idleAfter)
		if !loaded {
			continue
		}

		// Friend is idle-loaded: send width goal message
		takenReads, takenWork := friendCardCounts(s, f.Name)
		minsIdle := int(s.Now.Sub(s.FriendLastEvidence(f)).Minutes())
		width := f.Width

		msg := fmt.Sprintf("WIDTH: %s is loaded and idle: %d reads, %d work, width %d, idle %dm",
			f.Name, takenReads, takenWork, width, minsIdle)
		body := fmt.Sprintf("WIDTH %s: %s", f.Name, msg)

		// Add a note to the seat's inbox feed
		seatNote := fmt.Sprintf("friend %s idle-loaded %dm: width goal sent", f.Name, minsIdle)

		p.Notes = append(p.Notes, Note{
			Who:   r.who(),
			When:  s.Now,
			What:  seatNote,
		})

		sent++
	}

	return p, sent
}

// TickRuleFriendIdleReturn returns idle friends' cards to the pool and marks them idle.
func TickRuleFriendIdleReturn(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	returned := 0

	idleAfter := s.FriendIdleAfter()

	for _, f := range s.Friends {
		if f.Status != Up {
			continue
		}

		loaded, newestEvidence := IsIdleLoaded(s, f, idleAfter)
		if !loaded {
			continue
		}

		// Check if still idle for another idle bound
		if s.Now.Sub(newestEvidence) <= 2*idleAfter {
			continue
		}

		// Return cards to pool
		cards := friendLoadedCards(s, f.Name)
		var returns []string
		for _, c := range cards {
			if isRead(c) {
				// Return read to review
				returns = append(returns, c.ID)
			} else {
				// Return work to ready
				returns = append(returns, c.ID)
			}
		}

		if len(returns) == 0 {
			continue
		}

		// Mark friend as idle
		since := newestEvidence
		idleReason := fmt.Sprintf("idle %d minutes: %s by rule %s",
			int(since.Sub(since).Minutes()), newestEvidence.Format(time.RFC3339), RuleFriendIdleReturn)

		// Mark friend row as idle (not down)
		rowKey := fmt.Sprintf("row:%s", f.Name)
		s.Work.Set(rowKey, PropFriendIdle, "true")
		s.Work.Set(rowKey, PropFriendIdleSince, since.Format(time.RFC3339))

		// Add judgment to seat's inbox (one per row, not per card)
		p.Notes = append(p.Notes, Note{
			Who:   r.who(),
			When:  s.Now,
			What:  fmt.Sprintf("%s idle since %s by rule %s", f.Name, since.Format(time.RFC3339), RuleFriendIdleReturn),
		})

		returned += len(returns)
	}

	return p, returned
}

// friendCardCounts returns the number of taken read and work cards for a friend.
func friendCardCounts(s *Snapshot, friend string) (reads, work int) {
	for _, row := range s.Work.Rows() {
		if row != friend {
			continue
		}
		for _, c := range s.Work.Table[row] {
			if c.F(FieldTakenBy) == friend {
				if isRead(c) {
					reads++
				} else {
					work++
				}
			}
		}
	}
	return
}

// friendLastEvidence returns the time of the friend's newest evidence of work.
func (s *Snapshot) friendLastEvidence(f FriendSeat) time.Time {
	var newest time.Time

	// Check friend's activity
	if !f.Active.IsZero() {
		newest = f.Active
	}

	// Check cards for evidence
	for _, row := range s.Work.Rows() {
		if row != f.Name {
			continue
		}
		for _, c := range s.Work.Table[row] {
			if p := c.F(FieldProgress); p != "" {
				if t, ok := tryParseStamp(p); ok && t.After(newest) {
					newest = t
				}
			}
		}
	}

	return newest
}

// tryParseStamp tries to parse a timestamp string.
func tryParseStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

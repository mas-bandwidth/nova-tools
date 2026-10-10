package sprint

import (
	"fmt"
	"slices"
	"time"
)

// The two rules this file adds are registered here, not in rules.go, so the change stays
// inside the card's PATHS: the names nova-config's answer_rules_off takes and the tick parts
// that run them (docs/SPEC-SPRINT.md section 8, the rules table).
const (
	// RuleFriendIdle is the rule that detects a friend loaded but idle and sends the width goal.
	RuleFriendIdle = "friend-idle"
	// RuleFriendIdleReturn is the rule that hands a friend's idle cards back to the pool.
	RuleFriendIdleReturn = "friend-idle-return"
)

const (
	// PartRuleIdle is the tick part that runs the friend-idle rule.
	PartRuleIdle = "rule friend idle"
	// PartRuleIdleReturn is the tick part that runs the friend-idle-return rule.
	PartRuleIdleReturn = "rule friend idle return"
)

// init registers the two rules with RuleNames (held equal to nova-config's AnswerRules) and
// TickRules, before PartRuleBrief as every rule is before the brief defects.
func init() {
	RuleNames = append(RuleNames, RuleFriendIdle, RuleFriendIdleReturn)
	slices.Sort(RuleNames)
	idle := []TickPartDef{{PartRuleIdle, TickRuleIdle}, {PartRuleIdleReturn, TickRuleIdleReturn}}
	at := slices.IndexFunc(TickRules, func(d TickPartDef) bool { return d.Name == PartRuleBrief })
	if at < 0 {
		TickRules = append(TickRules, idle...)
		return
	}
	TickRules = slices.Insert(TickRules, at, idle...)
}

// evidenceField is the field that holds the latest evidence of work for a friend's row.
const evidenceField = "evidence"

// EvidenceField names the field that holds the latest evidence of work for a friend's row.
const EvidenceField = evidenceField

// BusMessageSubjectIdleLoaded is the subject for the width goal bus message.
const BusMessageSubjectIdleLoaded = "WIDTH: your row is loaded and idle"

// FieldFriendIdleSince is the field written on a friend's row when it is marked idle.
const FieldFriendIdleSince = "friend_idle_since"

// FieldFriendIdleReason is the reason a friend's row is marked idle.
const FieldFriendIdleReason = "friend_idle_reason"

// FieldFriendIdleLoaded is the timestamp when a friend's row was first detected idle-loaded.
const FieldFriendIdleLoaded = "friend_idle_loaded"

// evidenceTime returns the time of the latest evidence of work.
func evidenceTime(c *Card) time.Time {
	if c == nil {
		return time.Time{}
	}
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
	return IsFriendRow(row)
}

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

// friendTakenCounts returns the count of ready and working cards on a friend's row,
// split by reads and work.
func friendTakenCounts(s *Snapshot, row string) (reads, work int) {
	if s == nil || s.Fleet == nil {
		return 0, 0
	}
	for _, col := range []string{Ready, Working} {
		for _, c := range s.Fleet.Cell(row, col) {
			if c.F("kind") == "read" {
				reads++
			} else {
				work++
			}
		}
	}
	return reads, work
}

// WidthGoalText returns the width goal message text.
func WidthGoalText(friendName string, takenReads, takenWork, width int, idleMinutes int64) string {
	return fmt.Sprintf("Width goal for %s: you are holding %d reads and %d work cards (width %d). You have been idle for %d minutes.",
		friendName, takenReads, takenWork, width, idleMinutes)
}

// friendIsUp checks whether a friend's presence is up.
func friendIsUp(s *Snapshot, r TickReq, friend string, row string) bool {
	if len(r.Friends) > 0 {
		seat := findFriendSeat(r.Friends, friend)
		return seat != nil && seat.Status == Up
	}
	if s != nil && len(s.Friends) > 0 {
		seat := findFriendSeat(s.Friends, friend)
		return seat != nil && seat.Status == Up
	}
	if s != nil && s.Fleet != nil {
		if ctl := s.MemberCtl(row); ctl != nil {
			return ctl.F("status") == Up
		}
	}
	return false
}

// friendEvidenceOfWork returns the newest evidence of work for a friend.
func friendEvidenceOfWork(s *Snapshot, r TickReq, friend string, row string) time.Time {
	var newest time.Time
	see := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if s != nil && !s.Now.IsZero() && t.After(s.Now) {
			t = s.Now
		}
		if t.After(newest) {
			newest = t
		}
	}

	if s != nil && s.Fleet != nil {
		if ctl := s.MemberCtl(row); ctl != nil {
			see(evidenceTime(ctl))
			see(stampAt(ctl, "active"))
		}

		for _, col := range []string{Ready, Working} {
			for _, c := range s.Fleet.Cell(row, col) {
				see(stampAt(c, FieldProgress))
				see(stampAt(c, "taken"))
				see(stampAt(c, "dealt"))
				see(stampAt(c, FieldReported))
				see(stampAt(c, "read"))
				see(stampAt(c, "verdict_at"))
			}
		}

		for _, col := range []string{DoneOK, DoneFailed} {
			for _, c := range s.Fleet.Cell(row, col) {
				see(stampAt(c, "finished"))
				see(stampAt(c, FieldReported))
			}
		}
	}

	recent := func(t time.Time) bool {
		if t.IsZero() || s == nil || s.Now.IsZero() {
			return false
		}
		age := s.Now.Sub(t)
		return age >= 0 && age <= s.FriendIdleAfter()
	}

	for _, seat := range r.Friends {
		if seat.Name == friend {
			see(seat.Finished)
			see(seat.Active)
			if len(seat.Running) > 0 {
				if recent(seat.Beat.At) {
					see(seat.Beat.At)
				}
				if recent(seat.Active) {
					see(seat.Active)
				}
			}
		}
	}
	if s != nil {
		for _, seat := range s.Friends {
			if seat.Name == friend {
				see(seat.Finished)
				see(seat.Active)
				if len(seat.Running) > 0 {
					if recent(seat.Beat.At) {
						see(seat.Beat.At)
					}
					if recent(seat.Active) {
						see(seat.Active)
					}
				}
			}
		}
	}

	b, ok := r.Beats[friend]
	if !ok || b.Friend == nil {
		b, ok = r.Beats[row]
	}
	if ok && b.Friend != nil {
		see(b.Friend.Active)
		if (b.Friend.Working != nil && *b.Friend.Working > 0) || len(b.Friend.Running) > 0 {
			if recent(b.At) {
				see(b.At)
			}
			if recent(b.Friend.Active) {
				see(b.Friend.Active)
			}
		}
	}

	return newest
}

// isRowIdleLoaded computes whether a friend row is currently idle-loaded, and how many minutes idle.
func isRowIdleLoaded(s *Snapshot, r TickReq, row string, bound time.Duration) (bool, int64) {
	if !isFriendRow(row) {
		return false, 0
	}
	friend, ok := FriendOfRow(row)
	if !ok {
		return false, 0
	}
	if !friendIsUp(s, r, friend, row) {
		return false, 0
	}
	reads, work := friendTakenCounts(s, row)
	if reads+work == 0 {
		return false, 0
	}
	evidence := friendEvidenceOfWork(s, r, friend, row)
	if evidence.IsZero() {
		return false, 0
	}
	if s.Now.Sub(evidence) <= bound {
		return false, 0
	}
	idleMinutes := int64(s.Now.Sub(evidence) / time.Minute)
	return true, idleMinutes
}

// ruleFriendIdle computes whether a friend row should trigger the friend-idle rule.
// Kept for direct testing and compatibility.
func ruleFriendIdle(friends []FriendSeat, row string, fleet *Table, now time.Time, bound time.Duration) bool {
	s := &Snapshot{Now: now, Fleet: fleet}
	r := TickReq{Friends: friends}
	idle, _ := isRowIdleLoaded(s, r, row, bound)
	return idle
}

// friendWidth resolves the width for a friend.
func (s *Snapshot) friendWidth(r TickReq, friend string, row string) int {
	if seat := findFriendSeat(r.Friends, friend); seat != nil && seat.Width > 0 {
		return seat.Width
	}
	if seat := findFriendSeat(s.Friends, friend); seat != nil && seat.Width > 0 {
		return seat.Width
	}
	if s != nil && s.Fleet != nil {
		if ctl := s.MemberCtl(row); ctl != nil && ctl.F(FieldWidth) != "" {
			if w, err := ParseWidth(ctl.F(FieldWidth)); err == nil {
				return w
			}
		}
	}
	return 0
}

// TickRuleIdle sends width goal to friends who are loaded but idle.
func TickRuleIdle(s *Snapshot, r TickReq) (Plan, int) {
	if !r.AnswerRules || (s != nil && s.RuleOff(RuleFriendIdle)) {
		return Plan{}, 0
	}
	if s == nil || s.Fleet == nil {
		return Plan{}, 0
	}
	var p Plan
	bound := s.FriendIdleAfter()

	for _, row := range s.Fleet.Rows() {
		if !isFriendRow(row) {
			continue
		}
		friend, ok := FriendOfRow(row)
		if !ok {
			continue
		}

		ctl := s.MemberCtl(row)
		idle, idleMinutes := isRowIdleLoaded(s, r, row, bound)

		if !idle {
			// If previously marked idle-loaded, reset it when evidence becomes fresh
			if ctl != nil && ctl.F(FieldFriendIdleLoaded) != "" {
				p.Units = append(p.Units, Unit{
					Key:    ctl.ID,
					Stream: row,
					Changes: []Change{
						change(Fleet, setEntry(ctl, nil, FieldFriendIdleLoaded)),
					},
					Moved: fmt.Sprintf("friend %s evidence fresh: idle-loaded reset", friend),
				})
			}
			continue
		}

		// First time only
		if ctl != nil && ctl.F(FieldFriendIdleLoaded) != "" {
			continue
		}

		reads, work := friendTakenCounts(s, row)
		width := s.friendWidth(r, friend, row)

		// Judgment-free line to seat's inbox feed: "friend <f> idle-loaded <n>m: width goal sent"
		to := ""
		if s != nil {
			to = s.Coordinator
		}
		p.Notes = append(p.Notes, Note{
			Kind:   Happened,
			Type:   "inbox",
			Stream: row,
			What:   fmt.Sprintf("friend %s idle-loaded %dm: width goal sent", friend, idleMinutes),
			Who:    "rule " + RuleFriendIdle,
			At:     s.Now,
			To:     to,
		})

		if r.SendWidthGoal != nil {
			_ = r.SendWidthGoal(friend, reads, work, width, idleMinutes) // ignored: width goal send failure does not block rule execution
		}

		if ctl != nil {
			p.Units = append(p.Units, Unit{
				Key:    ctl.ID,
				Stream: row,
				Changes: []Change{
					change(Fleet, setEntry(ctl, map[string]string{
						FieldFriendIdleLoaded: stamp(s.Now),
					})),
				},
				Moved: fmt.Sprintf("friend %s idle-loaded %dm: width goal sent", friend, idleMinutes),
			})
		}
	}
	return p, 0
}

// TickRuleIdleReturn returns cards to pool when friend remains idle for a second bound.
func TickRuleIdleReturn(s *Snapshot, r TickReq) (Plan, int) {
	if !r.AnswerRules || (s != nil && s.RuleOff(RuleFriendIdleReturn)) {
		return Plan{}, 0
	}
	if s == nil || s.Fleet == nil {
		return Plan{}, 0
	}
	var p Plan
	bound := s.FriendIdleAfter()

	for _, row := range s.Fleet.Rows() {
		if !isFriendRow(row) {
			continue
		}
		friend, ok := FriendOfRow(row)
		if !ok {
			continue
		}

		idle, _ := isRowIdleLoaded(s, r, row, bound)
		if !idle {
			continue
		}

		ctl := s.MemberCtl(row)
		if ctl == nil || ctl.F(FieldFriendIdleLoaded) == "" {
			continue
		}
		loadedAt, err := time.Parse(time.RFC3339, ctl.F(FieldFriendIdleLoaded))
		if err != nil || s.Now.Sub(loadedAt) < bound {
			continue
		}

		nowStamp := stamp(s.Now)
		takenBackReason := RuleFriendIdleReturn + " at " + nowStamp

		for _, col := range []string{Ready, Working} {
			for _, c := range s.Fleet.Cell(row, col) {
				set := map[string]string{
					FieldTakenBack:  takenBackReason,
					FieldTakenFrom:  row,
					"reason":        "returned to pool by " + RuleFriendIdleReturn,
					"untaken_since": nowStamp,
				}
				u := Unit{
					Key:    c.ID,
					Stream: row,
					Changes: []Change{
						change(Fleet, moveEntry(c, row, Withdrawn, set, "taken", "dealt")),
					},
					Moved: fmt.Sprintf("%s returned to pool by rule %s", c.ID, RuleFriendIdleReturn),
				}
				if c.F("kind") != "read" && s.Work != nil {
					if pr := s.Work.Card(c.F("primary")); pr != nil && pr.Col == Working {
						u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")))
						u.Moved += "; " + pr.ID + " working -> ready"
					}
				}
				p.Units = append(p.Units, u)
			}
		}

		if ctl != nil {
			p.Units = append(p.Units, Unit{
				Key:    ctl.ID,
				Stream: row,
				Changes: []Change{
					change(Fleet, setEntry(ctl, map[string]string{
						"status":              "idle",
						FieldFriendIdleSince:  nowStamp,
						FieldFriendIdleReason: "idle-loaded for two bounds",
					}, FieldFriendIdleLoaded)),
				},
				Moved: fmt.Sprintf("friend %s row marked idle", friend),
			})
		}

		// The row's one judgment, built as NStatus is: the row is its primary
		// subject and it names no work stream, so the tick's stream-retirement
		// pass (TickRetireGone, NamedStream) leaves it open for the seat.
		p.Notes = append(p.Notes, Note{
			Kind:      Judgment,
			Type:      RuleFriendIdleReturn,
			Primaries: []string{row},
			Count:     1,
			What:      fmt.Sprintf("friend %s idle: cards returned to pool", friend),
			Who:       "rule " + RuleFriendIdleReturn,
			At:        s.Now,
		})
	}
	return p, 0
}

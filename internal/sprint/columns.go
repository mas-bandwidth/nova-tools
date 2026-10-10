package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The review reason (docs/SPEC-SPRINT.md section 1, "The review reason"): today every
// primary in review sits in one bucket, and the bucket mixes two jobs. A primary whose
// newest report is ok waits on a reader, or on a read already out: the machine's work.
// A primary the machine has judged "the brief is wrong, not the worker" waits on a person
// to re-cut the brief: the seat's work. The second never moves unless the seat remembers,
// and the owner, 2026-10-07, named it "another state that is not being visualized" and "a
// current hole where all our work goes to die".
//
// The card's fix for the next attempt is explicit: the work columns are a locked table
// shape (TABLES.lock), so there is NO new column. `review` keeps its primaries, and each
// one carries a review reason, FieldReviewReason, "read" or "defect". The reason is a view
// of state the sprint already keeps: a bound judgment open on the primary (NBriefWrong, or
// the brief defect NBriefDefect), the primary's own brief-defect mark (FieldBriefDefect),
// or a stored finding that names the brief (brief_defect.go). It is cleared by the steps
// that answer a brief: brief, recut and drop all leave the primary out of review, or clear
// the mark (steps_edit.go), so nothing else has to unset it.
//
// Every count review feeds is split "reads <n> · defect <n>" (where, view coordinator, the
// dashboard's JSON as review_reads and review_defect, card --all --json as the field), and
// the defect alarm (PropAlarmDefect, set --alarm-defect <n>, default AlarmDefectDefault)
// counts defect cards, not reads.

// FieldReviewReason is the field a primary in review carries: ReviewReasonRead or
// ReviewReasonDefect, "" outside review. It is a view, computed from state the sprint
// already keeps (ReviewReason), so every step that answers a brief clears it for free.
const FieldReviewReason = "review_reason"

// The two review reasons: the machine's work waiting on a read, and the seat's work
// waiting on a re-cut brief.
const (
	ReviewReasonRead   = "read"
	ReviewReasonDefect = "defect"
)

// The defect alarm (docs/SPEC-SPRINT.md section 8, "The defect alarm"): a count on the
// work table (set --alarm-defect <n>), on by default at AlarmDefectDefault. It is raised
// when more than n primaries in review are brief defects, or when any of them has waited
// in defect longer than DefectAlarmAge; it clears when neither holds.
const (
	PropAlarmDefect    = "alarm_defect"
	NAlarmDefect       = "defect above its alarm"
	AlarmDefectDefault = 10
	DefectAlarmAge     = 2 * time.Hour
)

// ReviewReason is the reason a primary in review waits: "" when it is not placed in
// review, ReviewReasonDefect when the brief is wrong and a person must re-cut it
// (ReviewDefect), else ReviewReasonRead. It reads only state the sprint keeps, so it is
// the same on every path and needs no field write.
func ReviewReason(s *Snapshot, c *Card) string {
	if s == nil || c == nil || !c.Placed() || c.Col != Review {
		return ""
	}
	if _, _, ok := ReviewDefect(s, c); ok {
		return ReviewReasonDefect
	}
	return ReviewReasonRead
}

// ReviewDefect is whether a primary in review is a brief defect, and since when: ok when
// one of the ways item 1 names stands, with the reason and the time it began.
//   - a bound judgment open on it, the brief is wrong not the worker (NBriefWrong), or a
//     brief defect (NBriefDefect); "since" is the judgment's time, so the age is real;
//   - its own brief-defect mark (FieldBriefDefect), a stamp when the rule set it and the
//     reason when a worker's HOLD named it; a stamp gives the age, a reason says now;
//   - its stored finding names the brief (BriefDefectOf: the base lacks a PATHS file, a
//     duplicate of landed work, a decision delivered, PATHS do not hold).
func ReviewDefect(s *Snapshot, c *Card) (why string, since time.Time, ok bool) {
	if s == nil || c == nil || !c.Placed() || c.Col != Review {
		return "", time.Time{}, false
	}
	for _, o := range s.Open {
		if o.Subject() != c.ID {
			continue
		}
		if o.Note.Type == NBriefWrong || o.Note.Type == NBriefDefect {
			return o.Note.What, o.Note.At, true
		}
	}
	if v := c.F(FieldBriefDefect); v != "" {
		if at, err := time.Parse(time.RFC3339, v); err == nil {
			return "a brief defect since " + v, at, true
		}
		return "a brief defect: " + v, s.Now, true
	}
	if f := c.F("finding"); f != "" {
		if reason := BriefDefectOf(f); reason != "" {
			return "its finding names the brief: " + reason, s.Now, true
		}
	}
	return "", time.Time{}, false
}

// ReviewSplit counts the review column by reason: reads waiting, and brief defects. Every
// count that review feeds is split this way (where, view coordinator, the dashboard).
func ReviewSplit(s *Snapshot) (reads, defect int) {
	for _, c := range s.Work.Column(Review) {
		if ReviewReason(s, c) == ReviewReasonDefect {
			defect++
		} else {
			reads++
		}
	}
	return reads, defect
}

// DefectCards are the primaries in review held on a brief defect, the oldest wait first,
// each with its reason (ReviewDefect): what the defect alarm lists.
func DefectCards(s *Snapshot) []*Card {
	type held struct {
		c     *Card
		since time.Time
	}
	var cards []held
	for _, c := range s.Work.Column(Review) {
		if _, since, ok := ReviewDefect(s, c); ok {
			cards = append(cards, held{c: c, since: since})
		}
	}
	slices.SortStableFunc(cards, func(a, b held) int {
		return a.since.Compare(b.since)
	})
	out := make([]*Card, len(cards))
	for i, h := range cards {
		out[i] = h.c
	}
	return out
}

// defectAlarmSetting is the defect alarm's threshold as the work table's property holds
// it: its count, or false when it is off. No property is the default, so the alarm is on
// unless the coordinator takes it off.
func defectAlarmSetting(s *Snapshot) (int, bool) {
	if s == nil || s.Work == nil {
		return 0, false
	}
	v, ok := s.Work.Prop(PropAlarmDefect)
	if !ok || v == "" {
		return AlarmDefectDefault, true
	}
	if v == AlarmOff {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return AlarmDefectDefault, true
	}
	return n, true
}

// DefectAlarm is the defect alarm's condition and what it says, ok when it is raised: more
// than its threshold in defect, or any defect older than DefectAlarmAge. what lists the
// oldest five with their reasons, as item 3 asks.
func DefectAlarm(s *Snapshot) (string, bool) {
	n, on := defectAlarmSetting(s)
	if !on {
		return "", false
	}
	cards := DefectCards(s)
	if len(cards) == 0 {
		return "", false
	}
	old := false
	var oldest []string
	for i, c := range cards {
		why, since, _ := ReviewDefect(s, c)
		age := s.Now.Sub(since)
		if age > DefectAlarmAge {
			old = true
		}
		if i < 5 {
			oldest = append(oldest, fmt.Sprintf("%s (%s in defect): %s", c.ID, defectAge(age), firstSentence(why)))
		}
	}
	switch {
	case len(cards) > n:
		return fmt.Sprintf("%d primaries in defect, above the alarm of %d: %s", len(cards), n, strings.Join(oldest, "; ")), true
	case old:
		return fmt.Sprintf("%d primaries in defect, one over %s: %s", len(cards), DefectAlarmAge, strings.Join(oldest, "; ")), true
	}
	return "", false
}

// defectAge is a defect wait as a person reads it: just now, minutes, or hours and minutes.
func defectAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// briefDefectRemedy is the remedy the defect judgment carries (item 4): the brief corrected
// in place, its next attempt, or the card dropped. The finding to answer rides on the
// judgment's own words, so the seat pastes one line.
func briefDefectRemedy(id string) string {
	return "run: nova-sprint brief " + id + " --brief-file <path> (the brief corrected in place, its next attempt), or nova-sprint drop " + id + " --reason '<why>'"
}

// ReviewCardFields is a primary's stored fields with its review reason added, the shape
// card --all --json carries: the fields map as the card has it, FieldReviewReason set to
// ReviewReasonRead or ReviewReasonDefect while it is in review. The command should marshal
// this instead of the raw fields map (a proposed diff on cmd/nova-sprint reads.go, outside
// this card's PATHS).
func ReviewCardFields(s *Snapshot, c *Card) map[string]string {
	out := map[string]string{}
	if c != nil {
		maps.Copy(out, c.Fields)
	}
	if r := ReviewReason(s, c); r != "" {
		out[FieldReviewReason] = r
	}
	return out
}

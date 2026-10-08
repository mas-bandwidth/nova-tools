package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Review is two states in one column (docs/SPEC-SPRINT.md section 6, "review is reads and
// defect"; the owner, 2026-10-07: "this feels like another state that is not being
// visualized" and "it also feels like a current hole where all our work goes to die"). A
// card in review carries FieldReviewReason: ReviewReasonRead for an ok report waiting on or
// under a read (the machine's work), and ReviewReasonDefect for one whose brief is wrong,
// not the worker (the seat's work): a bound judgment "the brief is wrong", the same failure
// reason across two attempts, or a reader's finding that names the brief. The work table's
// locked shape keeps the column (schema.go, TABLES.lock); the field is how the split is
// carried and counted everywhere review is.
const (
	// FieldReviewReason is a review card's reason for being there, and FieldReviewReasonAt
	// is when it entered: the stamp the defect alarm counts its age from.
	FieldReviewReason   = "review_reason"
	FieldReviewReasonAt = "review_reason_at"

	// ReviewReasonRead is an ok report waiting on or under a read.
	ReviewReasonRead = "read"
	// ReviewReasonDefect is a brief the machine judged wrong, not the worker: the card
	// waits on a person to re-cut the brief, never on the machine.
	ReviewReasonDefect = "defect"
)

// ReviewReasonOf is the reason c is in review: defect when its brief is wrong (the
// FieldReviewReason the rules wrote, or the FieldBriefDefect mark they leave for a card
// admitted before the field), read for an ok report waiting on or under a read. "" for a
// card that is not placed in review.
func ReviewReasonOf(c *Card) string {
	if c == nil || !c.Placed() || c.Col != Review {
		return ""
	}
	switch c.F(FieldReviewReason) {
	case ReviewReasonDefect:
		return ReviewReasonDefect
	case ReviewReasonRead:
		return ReviewReasonRead
	}
	if c.F(FieldBriefDefect) != "" {
		return ReviewReasonDefect
	}
	return ReviewReasonRead
}

// ReviewSplit is the work table's review cards by reason: reads (an ok report waiting on or
// under a read) and defect (the brief is wrong, not the worker). It is what where, the
// coordinator view and the dashboard show as `reads <n> · defect <n>`, and the defect
// alarm counts.
func ReviewSplit(work *Table) (reads, defect int) {
	if work == nil {
		return 0, 0
	}
	for _, c := range work.Column(Review) {
		if ReviewReasonOf(c) == ReviewReasonDefect {
			defect++
		} else {
			reads++
		}
	}
	return reads, defect
}

// ReviewDefectCards is the work table's review cards whose brief is wrong, in work order.
func ReviewDefectCards(work *Table) []*Card {
	if work == nil {
		return nil
	}
	var out []*Card
	for _, c := range work.Column(Review) {
		if ReviewReasonOf(c) == ReviewReasonDefect {
			out = append(out, c)
		}
	}
	return out
}

// DefectAge is how long c has been in defect: now less FieldReviewReasonAt, else the
// FieldBriefDefect stamp it was marked with, else 0 (a card with no readable stamp counts
// as just entered, never as old).
func DefectAge(c *Card, now time.Time) time.Duration {
	if c == nil {
		return 0
	}
	for _, f := range []string{FieldReviewReasonAt, FieldBriefDefect} {
		if at, err := time.Parse(time.RFC3339, c.F(f)); err == nil {
			if at.After(now) {
				return 0
			}
			return now.Sub(at)
		}
	}
	return 0
}

// DefectReason is what c's defect says: the FieldBriefDefect reason the worker or the rule
// named, else "the brief is wrong, not the worker".
func DefectReason(c *Card) string {
	if c != nil {
		if r := c.F(FieldBriefDefect); r != "" {
			return r
		}
	}
	return "the brief is wrong, not the worker"
}

// MaxDefectList is how many defect cards the alarm names, oldest first.
const MaxDefectList = 5

// DefectList is the oldest n defect cards, one line each: the id, its reason and its age,
// as the alarm names them. Fewer than n cards names them all.
func DefectList(cards []*Card, now time.Time, n int) []string {
	sorted := append([]*Card(nil), cards...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return DefectAge(sorted[i], now) > DefectAge(sorted[j], now)
	})
	var out []string
	for _, c := range sorted {
		if len(out) == n {
			break
		}
		out = append(out, fmt.Sprintf("%s (%s, %s)", c.ID, cutText(DefectReason(c), 120), shortAge(DefectAge(c, now))))
	}
	return out
}

// shortAge is a duration as the alarm's lines read it: minutes under two hours, else hours.
func shortAge(d time.Duration) string {
	if d < 2*time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1fh", d.Hours())
}

// DefectAlarm is the defect alarm's condition: more than n cards in defect, or any one card
// in defect longer than DefectAlarmAge. what is the judgment's line, "" when neither holds.
// The oldest MaxDefectList cards are listed with their reasons.
func DefectAlarm(work *Table, n int, now time.Time) (what string) {
	cards := ReviewDefectCards(work)
	over := n >= 0 && len(cards) > n
	old := time.Duration(0)
	var oldest *Card
	for _, c := range cards {
		if age := DefectAge(c, now); age > old {
			old, oldest = age, c
		}
	}
	if !over && old <= DefectAlarmAge {
		return ""
	}
	line := fmt.Sprintf("%d primaries in defect, above the alarm of %d", len(cards), n)
	if !over {
		line = fmt.Sprintf("%s has been in defect %s, longer than %s", oldest.ID, shortAge(old), DefectAlarmAge)
	}
	if oldest != nil && over {
		line += fmt.Sprintf("; the oldest, %s, has been in defect %s", oldest.ID, shortAge(old))
	}
	if listed := DefectList(cards, now, MaxDefectList); len(listed) > 0 {
		line += "; oldest: " + strings.Join(listed, "; ")
	}
	return line + "; the brief is wrong, not the worker: re-cut the brief or drop the card; run: nova-sprint where"
}

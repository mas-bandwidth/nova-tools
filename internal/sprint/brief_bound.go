package sprint

import (
	"fmt"
	"strings"
)

// The brief's bound (docs/SPEC-SPRINT.md, "The brief is wrong, not the worker"; the owner,
// 2026-10-03, after two gating cards were reworked to attempts 262 and 17 with the same reader
// finding every time: "These two gating cards getting rejected, should have been escalated to
// you, the coordinator, way sooner than this"). The same finding twice means the brief is
// wrong, not the worker, and no further attempt is possible without changing the brief: rework
// refuses such a card, whoever answers, and the judgment raised for it offers brief and drop,
// never rework. The decision is one pure function over the primary, AtBriefBound; a changed
// brief (Brief, FieldBriefAttempt) resets it.

// FieldFindingAttempt is the attempt whose broken reads' finding the primary's `finding`
// carries (Rework writes both: the attempt it sends back and what its readers found). A
// primary with a finding and no attempt recorded (admitted before this field) was reworked
// from the attempt before its current one.
const FieldFindingAttempt = "finding_attempt"

// FieldBriefAttempt is the primary's attempt when its brief was last replaced (Brief); absent,
// the brief is the one add gave it, at attempt 0.
const FieldBriefAttempt = "brief_attempt"

// MaxAttemptsPerBrief is how many attempts one brief may run: past it the brief is wrong,
// not the worker, whatever each attempt found.
const MaxAttemptsPerBrief = 5

// BriefBound is why a primary's brief is wrong: two attempts since the brief last changed
// whose readers found the same thing (Attempts and Finding), or more than MaxAttemptsPerBrief
// attempts since it changed (Since, with Finding "").
type BriefBound struct {
	ID       string
	Attempts [2]int // the two attempts that failed the same way, in order
	Finding  string // the finding's first sentence, as the first of the two said it
	Since    int    // the attempts since the brief last changed, when that is the bound
}

// String is the bound said in one line: what repeated and where, then the verdict.
func (b BriefBound) String() string {
	if b.Finding != "" {
		return fmt.Sprintf("%s has failed the same way twice (attempts %d and %d: %s); the brief is wrong, not the worker",
			b.ID, b.Attempts[0], b.Attempts[1], b.Finding)
	}
	return fmt.Sprintf("%s has made %d attempts since its brief last changed (its bound is %d); the brief is wrong, not the worker",
		b.ID, b.Since, MaxAttemptsPerBrief)
}

// Remedy is what changes the brief: replaced in place while the card waits, else dropped and
// added again corrected. A rework's --fix changes the brief not at all, so it is never offered.
func (b BriefBound) Remedy() string {
	return "run: nova-sprint brief " + b.ID + " --brief-file <path> (a waiting card) or drop " + b.ID + " and add it again with the brief corrected"
}

// Why is the rework's refusal of a card at the bound: the line, and the remedy.
func (b BriefBound) Why() string { return b.String() + "; " + b.Remedy() }

// FindingClass is what two findings are compared by: the finding's first sentence (its first
// line, up to the first ". "), its whitespace collapsed and its case folded; and a finding of
// files outside the card's PATHS, however it is worded ("files outside PATHS", "outside its
// PATHS"), is the one class `files outside paths`. "" for an empty finding.
func FindingClass(finding string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(finding), "\n")
	if i := strings.Index(line, ". "); i >= 0 {
		line = line[:i]
	}
	line = strings.ToLower(strings.Join(strings.Fields(line), " "))
	if strings.Contains(line, "outside") && strings.Contains(line, "paths") {
		return "files outside paths"
	}
	return line
}

// SameFinding says two findings are the same finding: each has a class and it is the same one.
func SameFinding(a, b string) bool {
	c := FindingClass(a)
	return c != "" && c == FindingClass(b)
}

// firstSentence is a finding's first sentence as said, for the line that names it.
func firstSentence(finding string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(finding), "\n")
	if i := strings.Index(line, ". "); i >= 0 {
		line = line[:i+1]
	}
	return cutText(strings.Join(strings.Fields(line), " "), MaxProviderErrorBytes)
}

// AtBriefBound is whether the primary c is at its brief's bound, and why. finding is what
// its readers found at its current attempt: the broken reads' findings the store holds
// (brokenFindings), or the one a read step is about to write; "" when none is known. The
// finding before is the primary's own (`finding`, at FieldFindingAttempt), counted only
// when that attempt ran on the current brief. The count is the attempts since the brief
// last changed; a card never dealt is at no bound.
func AtBriefBound(c *Card, finding string) (BriefBound, bool) {
	attempt := c.Int("attempt")
	briefAt := c.Int(FieldBriefAttempt)
	if attempt == 0 {
		return BriefBound{}, false
	}
	prev, prevAt := c.F("finding"), c.Int(FieldFindingAttempt)
	if prev != "" && prevAt == 0 {
		prevAt = attempt - 1
	}
	if prev != "" && prevAt > briefAt && prevAt < attempt && SameFinding(prev, finding) {
		return BriefBound{ID: c.ID, Attempts: [2]int{prevAt, attempt}, Finding: firstSentence(prev)}, true
	}
	if since := attempt - briefAt; since > MaxAttemptsPerBrief {
		return BriefBound{ID: c.ID, Since: since}, true
	}
	return BriefBound{}, false
}

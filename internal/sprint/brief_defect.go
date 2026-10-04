package sprint

import (
	"regexp"
	"strings"
)

// A brief defect (docs/SPEC-SPRINT.md section 1, a brief defect): a worker's HOLD whose
// reason names the brief as the cause, so no worker could have done the card as cut: the
// base lacks what it builds on, it duplicates landed work, or it needs a decision not made.
// It counts against the stream that cut the card, never against the worker's ok%, and its
// judgment asks the coordinator to re-cut the brief, never to redeal it.

// The reasons a brief defect is recorded with (FieldBriefDefect), as BriefDefectOf reads them.
const (
	BriefDefectBase      = "the base lacks what the card builds on"
	BriefDefectDuplicate = "a duplicate of landed work"
	BriefDefectDecision  = "a decision not made"
	BriefDefectOther     = "named by the worker"
)

// FieldBriefDefect is the reason a work card, and its primary, ended on a brief defect;
// FieldBriefDefects is a stream's count of them, on its control card, bumped by one in the
// finish that records each (a Bump, so two in one step add up).
const (
	FieldBriefDefect  = "brief_defect"
	FieldBriefDefects = "brief_defects"
)

// briefDefectRE finds the label a worker names a brief defect with, and the word before it
// that would negate it ("not a brief defect", "no brief defect").
var briefDefectRE = regexp.MustCompile(`(?i)(\b(?:not|no)\s+(?:a\s+)?)?\bbrief[ -]defect\b`)

// BriefDefectOf is the brief defect a failed finish's report names: "" when it names none,
// else its reason. The report names one with the label "brief defect" (docs/SPEC-SPRINT.md
// section 1), and a negated label ("not a brief defect", "no brief defect") is no label;
// the reason is read from the words after the label: a duplicate or already landed work, a
// base that lacks a file, function or PR, or a decision, else the worker's own words.
func BriefDefectOf(report string) string {
	for _, m := range briefDefectRE.FindAllStringSubmatchIndex(report, -1) {
		if m[2] >= 0 {
			continue // negated
		}
		rest := strings.ToLower(report[m[1]:])
		switch {
		case strings.Contains(rest, "duplicate"), strings.Contains(rest, "already landed"):
			return BriefDefectDuplicate
		case strings.Contains(rest, "not on"), strings.Contains(rest, "lacks"), strings.Contains(rest, "missing"),
			strings.Contains(rest, "does not exist"), strings.Contains(rest, "do not exist"):
			return BriefDefectBase
		case strings.Contains(rest, "decision"):
			return BriefDefectDecision
		}
		return BriefDefectOther
	}
	return ""
}

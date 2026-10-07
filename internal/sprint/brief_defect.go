package sprint

import (
	"regexp"
	"strings"
)

// A brief defect (docs/SPEC-SPRINT.md section 1, a brief defect): a worker's HOLD whose
// reason names the brief as the cause, so no worker could have done the card as cut: the
// base lacks a PATHS file (or another thing the card builds on), it duplicates landed work,
// or it is a decision delivered. It counts against the stream that cut the card, never
// against the worker's ok%, and its judgment asks the coordinator to re-cut the brief,
// never to redeal it.

// The reasons a brief defect is recorded with (FieldBriefDefect), as BriefDefectOf reads them.
const (
	BriefDefectBase      = "the base lacks a PATHS file"
	BriefDefectDuplicate = "a duplicate of landed work"
	BriefDefectDecision  = "a decision delivered"
	BriefDefectOther     = "named by the worker"
)

// FieldBriefDefect (rules.go, the field the brief-defect rule stamps) also carries, on a work
// card and its primary, the reason a worker's HOLD named; FieldBriefDefects is a stream's count
// of them, on its control card, bumped by one in the finish that records each (a Bump, so two
// in one step add up).
const FieldBriefDefects = "brief_defects"

// negated is the words before a reason or the label that turn it into its opposite
// ("not a duplicate of landed work", "no brief defect"); the reason's own article is in it.
const negated = `(\b(?:not|no)\s+(?:an?\s+|the\s+)?)?`

// briefDefectReasons are the three reasons, each a detector on its own (docs/SPEC-SPRINT.md
// section 1, a brief defect): the words a worker states the reason in, matched as words, so
// a card id that holds them joined by hyphens is not one.
var briefDefectReasons = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)` + negated + `\b(?:the\s+)?base\s+lacks\b|\b(?:not|missing)\s+(?:on|from)\s+the\s+base\b|\b(?:does|do)\s+not\s+exist\s+on\s+the\s+base\b`), BriefDefectBase},
	{regexp.MustCompile(`(?i)` + negated + `\bduplicates?\s+(?:of\s+)?landed\s+work\b`), BriefDefectDuplicate},
	{regexp.MustCompile(`(?i)` + negated + `\bdecision\s+(?:(?:is|was|has\s+been|already)\s+)*delivered\b`), BriefDefectDecision},
}

// briefDefectLabel is the label a worker may name a brief defect with when its words are
// none of the three reasons: "brief defect", two words, never a hyphenated token.
var briefDefectLabel = regexp.MustCompile(`(?i)` + negated + `\bbrief\s+defect\b`)

// BriefDefectOf is the brief defect a failed finish's report names: "" when it names none,
// else its reason (docs/SPEC-SPRINT.md section 1, a brief defect). Each of the three reasons
// alone names one, the earliest in the report winning: the base lacks a PATHS file ("the
// base lacks", "not on the base", "does not exist on the base"), a duplicate of landed work,
// a decision delivered. The label "brief defect" with none of them names one in the
// worker's own words. A negated reason or label ("not a duplicate of landed work", "no
// brief defect") names none, and a hyphenated token (a card id such as
// hold-is-a-brief-defect) is no label.
func BriefDefectOf(report string) string {
	reason, at := "", len(report)
	for _, r := range briefDefectReasons {
		if i := firstUnnegated(r.re, report); i >= 0 && i < at {
			reason, at = r.reason, i
		}
	}
	if reason != "" {
		return reason
	}
	if firstUnnegated(briefDefectLabel, report) >= 0 {
		return BriefDefectOther
	}
	return ""
}

// firstUnnegated is where re first matches report with no negation before it, else -1. A
// match whose words are joined to the text before it by a hyphen is part of a token, not
// words, and does not count.
func firstUnnegated(re *regexp.Regexp, report string) int {
	for _, m := range re.FindAllStringSubmatchIndex(report, -1) {
		if m[2] >= 0 {
			continue // negated
		}
		if m[0] > 0 && strings.HasPrefix(report[m[0]-1:], "-") {
			continue // a hyphenated token
		}
		if m[1] < len(report) && report[m[1]] == '-' {
			continue
		}
		return m[0]
	}
	return -1
}

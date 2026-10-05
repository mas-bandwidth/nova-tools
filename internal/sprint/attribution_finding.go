package sprint

import (
	"regexp"
	"strings"
)

// AttributionRefusal is the remedy a `read --broken` whose finding is only
// about attribution is refused with (docs/SPEC-SPRINT.md, reader-ignores-attribution).
const AttributionRefusal = "attribution is never a finding (docs/SPEC-SPRINT.md); read --ok, or name the defect in the work"

var (
	// attributionSentenceSplit cuts a finding into sentences: a stop before
	// white space, a semicolon, a newline. A dot inside "5.5" or a file name
	// has no white space after it and does not cut.
	attributionSentenceSplit = regexp.MustCompile(`[.!?]\s+|[;\n]+`)
	// attributionStrong names attribution by itself: the trailer, the By:
	// line, the noreply address, the word.
	attributionStrong = regexp.MustCompile(`(?i)co-authored-by|\btrailers?\b|\bby:|noreply@|\battribution\b|\bharness\b`)
	// attributionModel is the model named, which is attribution only beside
	// the act of naming it (a sentence about a model the work reads is not).
	attributionModel = regexp.MustCompile(`(?i)\b(actual|named?|names|naming|claims?|claimed|your|which|wrong|true|real)\b.*\bmodels?\b|\bmodels?\b.*\b(named|name|names|claims?|claimed|inside the email|brackets)\b`)
)

// AttributionOnly reports whether every sentence of a reader's finding is
// about attribution: the commit trailer, the By: line, the model or harness
// named, or a request to amend them. A finding with any sentence about the
// work (or an empty one) is not attribution-only: a defect plus an
// attribution remark is kept. A worker names the model and harness it
// actually ran, which may differ from what a brief guessed; that is honesty,
// never a defect.
func AttributionOnly(finding string) bool {
	n := 0
	for _, s := range attributionSentenceSplit.Split(finding, -1) {
		s = strings.TrimSpace(s)
		if s == "" || strings.Trim(s, ".… ") == "" {
			continue
		}
		n++
		// A citation (file:line) does not make a sentence about the work: a
		// trailer sentence that points at the brief's line is still about the
		// trailer. A sentence with no attribution word in it is about the work.
		if !(attributionStrong.MatchString(s) || attributionModel.MatchString(s)) {
			return false
		}
	}
	return n > 0
}

package decide

import "strings"

// The read decision (SPEC-NOVA-DECIDE section 6): the first read of a worker's
// diff against the card that asked for it. Its inputs are the card text and
// the unified diff, and optionally one rule text the reader holds the diff to;
// nothing else is consulted.

// ReadName is the read decision's name in the record.
const ReadName = "read"

// The read's verdicts.
const (
	Land   = "LAND"
	Bounce = "BOUNCE"
	Unsure = "UNSURE"
)

// ReadSchema is the read's five questions.
func ReadSchema() Schema {
	return Schema{Name: ReadName, Questions: map[string]Question{
		"does_task": {Type: Noul, Instructions: "The DIFF does what the CARD's task states, for the lines the card lists, " +
			"and with the meaning the card asks to keep."},
		"lines_changed": {Type: Noul, Instructions: "Every line the CARD lists is changed by the DIFF, " +
			"or the card's own words allow it to stay."},
		"inside_paths": {Type: Noul, Instructions: "Every file the DIFF changes is named in the CARD's PATHS line, " +
			"or is a file the card itself names as one to update (a ledger, a renamed file's references)."},
		"defect": {Type: Noul, Instructions: "The DIFF introduces a defect: a behaviour change the card did not ask for " +
			"(an assertion that no longer fails when the old one did, a weakened test), broken text (a sentence fragment, " +
			"a line opening with a comma, an unmatched backquote, a comment that contradicts the code beside it, an " +
			"invented reason), or a lost reference (a test name, a file path, a rule number of a list the document still " +
			"numbers, an issue number or date that is data a test or format asserts, a measured record turned into a claim)."},
		"verdict": {Type: Choice, Instructions: "The first read's verdict on this DIFF as the CARD's answer.", Criteria: map[string]string{
			Land:   "the diff does the card's task, inside its paths, and introduces no defect",
			Bounce: "the diff misses the task, leaves a listed line, changes a file outside its paths, or introduces a defect",
			Unsure: "the card and the diff alone cannot settle it; a stronger reader is needed",
		}},
	}}
}

// ReadState is the text the read is asked over: the card, the rule when one
// is given, then the diff, each under its own heading.
func ReadState(card, diff, rule string) string {
	var b strings.Builder
	b.WriteString("CARD (the whole task the worker was given):\n")
	b.WriteString(strings.TrimRight(card, "\n"))
	if strings.TrimSpace(rule) != "" {
		b.WriteString("\n\nRULE (the reader holds the diff to this as well):\n")
		b.WriteString(strings.TrimRight(rule, "\n"))
	}
	b.WriteString("\n\nDIFF (what the worker committed, unified):\n")
	b.WriteString(strings.TrimRight(diff, "\n"))
	b.WriteString("\n")
	return b.String()
}

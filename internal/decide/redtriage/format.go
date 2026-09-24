package redtriage

// format.go is the shape of one printed row: one-line per field, no field
// re-ordered, every value escaped for a shell but not quoted (the reader
// pastes back into --test <Test>). A failure to be readable is a failure to
// be auditable, and the DONE-WHEN measurement joins fields by name.

import (
	"strconv"
	"strings"
)

// onelineField is the field's print, with a tab and a newline stripped so
// the field cannot author a second line; a space is its own field and is
// kept as one (a test name has no space, the field is read by a grep).
func onelineField(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// itoa is strconv.Itoa without the package's error path; a negative n is
// signed so the caller's bug surfaces as a printed minus rather than a
// silent zero. PRNumber carries it through to a "rowan/nova-tools#-1"
// that no field reader would accept, which is the point.
func itoa(n int) string { return strconv.Itoa(n) }

// ftoa is the confidence's print: two decimal places fixed so two
// Confidence values are comparable by eye in the audit sample
// (0.65, 0.78, 0.91), and so a 1.0 answer is `1.00` and a missing answer
// is `0.00` rather than `-`. The floor and confidence columns are joined
// on this printed shape.
func ftoa(f float64) string {
	if f == 0 {
		return "0.00"
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// whyOrDash prints the caller's why OR "-" when the why is empty, so an
// attribution that stands reads as `why=-` and an escalated one reads as
// `why=below-floor` (or `why=held-out`, `why=no-candidate`). The audit
// joins on the printed word.
func whyOrDash(w string) string {
	w = strings.TrimSpace(w)
	if w == "" {
		return "-"
	}
	return w
}

// boolYesNo prints the held-out bit: `yes` or `no`. A Yes/No rather than
// true/false keeps the field a single token a shell can parse with `cut
// -d' ' -f11` against the line.
func boolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

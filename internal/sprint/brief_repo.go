package sprint

import (
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A brief's REPO line is exactly owner/name (docs/SPEC-SPRINT.md, the card decides its
// model; docs/SPEC-CARD-CONTRACT.md section 6): the tier a writer stamps goes on the card's
// RESULT line, never on the REPO line, because the friend's staging reads the whole value
// and refuses "its REPO %q is no owner/name" (internal/friend/stage.go), leaving the card
// unstageable while it sits on her row.

// RepoLine is the value of a brief's REPO: line as written, and whether the brief names one;
// the reader is cardhdr.Value, the one reader of a card header line.
func RepoLine(brief string) (value string, ok bool) {
	return cardhdr.Value(brief, "REPO")
}

// RepoLineWhy is why a brief's REPO: line is no one repository, "" when the brief names none
// or reads as one. The value is named whole, so the word it gained (the tier a writer
// appended) is read: add holds it through card.Checks, and recut and rework refuse it here
// before anything is written.
func RepoLineWhy(brief string) string {
	value, ok := RepoLine(brief)
	if !ok || cardhdr.IsRepoValue(value) {
		return ""
	}
	return "REPO " + value + " is no owner/name; write REPO: <owner>/<name> alone"
}

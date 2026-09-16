package pulse

// THE CUT STAMP: A CARD NOBODY CUT IS A CARD NOBODY LAUNCHES. Pit stop 3, class P (#828).
//
// Every hand cut differed. Four shell scripts and a person wrote cards with four line-1
// shapes and their own numbering, two of them collided, and the cards that came out of them
// were indistinguishable at launch from the cards `cut` had written -- so nothing could
// refuse them. `cut --kind` is the one cutter (cutkind.go); this file is what makes that
// enforceable: the cutter signs what it wrote, and `launch` admits only a signed card.
//
// The stamp is the card's LAST line and nothing else is below it:
//
//	CUT: nova-pulse <version> <sha8 of the card body>
//
// The body is every byte above that line. So a card edited after it was stamped -- a step
// added, a path corrected, a deadline moved -- no longer matches its own sha8 and is
// refused by the same check, naming what happened. That is the half of the class that a
// mere "was it cut" flag would have missed: the hand cuts that hurt most were edits to a
// cut card, not cards written from nothing.
//
// --unstamped-ok is the migration week and nothing more: it admits an unstamped card and
// SAYS SO on its own line, so a bench still running it appears in the log rather than in
// somebody's memory. It never admits a card whose stamp is WRONG: an unsigned card is old,
// a mis-signed one was edited, and only the first of those is a migration.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// StampPrefix opens the stamp line. It names the tool, because a queue holds cards cut by
// more than one of them.
const StampPrefix = "CUT: nova-pulse "

// Stamp returns the card body with its stamp line appended. A body that already carries a
// stamp is re-stamped, never double-stamped: StampBody strips before the sha is taken.
func Stamp(card, version string) string {
	body := StampBody(card)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + stampLine(body, version)
}

// stampLine is the one line, with the version reduced to one token so the line is three
// tokens whatever a -X held.
func stampLine(body, version string) string {
	return fmt.Sprintf("%s%s %s\n", StampPrefix, stampVersion(version), sha8(body))
}

// stampVersion is the version as one token; an empty version is `devel`, which is what a
// build with no stamp of its own already calls itself.
func stampVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "devel"
	}
	return strings.Join(strings.Fields(v), "-")
}

// StampBody is the card without its stamp line: the bytes the sha8 is over. A card with no
// stamp is its own body, so this is safe to call on anything.
func StampBody(card string) string {
	body, _, ok := splitStamp(card)
	if !ok {
		return card
	}
	return body
}

// splitStamp cuts a card into its body and its stamp line. The stamp is the LAST non-empty
// line: a card whose stamp is followed by more text is a card with something appended after
// it was signed, and that is not a stamped card.
func splitStamp(card string) (body, stamp string, ok bool) {
	trimmed := strings.TrimRight(card, "\n")
	idx := strings.LastIndex(trimmed, "\n")
	last := trimmed
	if idx >= 0 {
		last = trimmed[idx+1:]
	}
	if !strings.HasPrefix(last, StampPrefix) {
		return card, "", false
	}
	if idx < 0 {
		return "", last, true
	}
	return trimmed[:idx+1], last, true
}

// StampCheck is what launch learns about one card: whether it carries a stamp this tool
// wrote, and when it does not, the one line saying why and what to do.
type StampCheck struct {
	Stamped bool   // a stamp line is present at all
	Valid   bool   // the stamp is present AND its sha8 is the body's
	Version string // the version the stamp names, when there is one
	Reason  string // empty when Valid
}

// CheckStamp reads a card's stamp. Its three answers are the three states a card can be in,
// and each has a different remedy, so each gets its own sentence.
func CheckStamp(card string) StampCheck {
	body, stamp, ok := splitStamp(card)
	if !ok {
		return StampCheck{Reason: "the card carries no cut stamp (cut it with nova-pulse cut --kind ..., or pass --unstamped-ok for the migration week)"}
	}
	fields := strings.Fields(stamp)
	// `CUT:` `nova-pulse` `<version>` `<sha8>`
	if len(fields) != 4 {
		return StampCheck{Stamped: true, Reason: "the cut stamp is not `" + strings.TrimSpace(StampPrefix) + " <version> <sha8>` (cut the card again with nova-pulse cut --kind ...)"}
	}
	version, got, want := fields[2], fields[3], sha8(body)
	if got != want {
		return StampCheck{Stamped: true, Version: version, Reason: fmt.Sprintf("the card body was edited after it was cut: stamp says %s, the body is %s (cut the card again with nova-pulse cut --kind ..., do not edit a cut card)", got, want)}
	}
	return StampCheck{Stamped: true, Valid: true, Version: version}
}

// sha8 is the first eight hex of the SHA-256 of a card body. Eight is enough to catch an
// edit, which is what this check is for; it is not a signature and does not pretend to be.
func sha8(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:8]
}

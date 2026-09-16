package pulse

import (
	"os"
	"path/filepath"
	"strings"
)

// Admit is the pit stop's admission gate (rule C of #828): a pure function that decides
// whether a card whose first line is cardLine1 may be launched. `stop` is the raw bytes of
// the STOP file; nil or empty (no stop open) admits every card.
//
// While a STOP exists, launch admits only cards whose line 1 names the issue or test on the
// STOP file's second line, and refuses the rest. The STOP file's first line is a heading;
// its second line is the one name — an issue such as "#694" or a test such as
// "TestJoinChildDeath" — that a fix card's line 1 must name. A STOP whose second line is
// absent or blank names nothing, so nothing is admitted and the reason says why.
func Admit(stop []byte, cardLine1 string) (ok bool, reason string) {
	if strings.TrimSpace(string(stop)) == "" {
		return true, ""
	}
	name := stopName(stop)
	if name == "" {
		return false, "STOP has no second line naming the issue or test, so no card is admitted"
	}
	if strings.Contains(cardLine1, name) {
		return true, ""
	}
	return false, "does not name " + name + " (the issue or test the stop admits)"
}

// stopName is the STOP file's second line, trimmed, or "" when there is none.
func stopName(stop []byte) string {
	lines := strings.Split(string(stop), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// readStop returns the raw bytes of <root>/STOP, or nil when no stop is open.
func readStop(root string) []byte {
	raw, err := os.ReadFile(filepath.Join(root, "STOP"))
	if err != nil {
		return nil
	}
	return raw
}

// cardLine1 returns a card's first line, trimmed, or "" for empty input.
func cardLine1(raw []byte) string {
	s := string(raw)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

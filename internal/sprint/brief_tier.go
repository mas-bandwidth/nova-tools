package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The tier a card is admitted with is written on its card's own line, the RESULT line the
// card contract carries it on (docs/SPEC-CARD-CONTRACT.md section 6): a store or transport
// header (REPO:, BASE:, STATUS:) is read as written by the staging and the frame, so the
// tier word is never appended to one (docs/SPEC-SPRINT.md, the card decides its model).

// storeHeader is a card-header key whose value a store or a transport reads as written.
var storeHeader = map[string]bool{"REPO": true, "BASE": true, "STATUS": true}

// tieredBrief is brief with " tier: <t>" on the line the tier belongs on: the brief's
// RESULT: line when it has one, else line 1 when line 1 is the card's own (a title, not a
// store header). A brief whose own line carries no tier cannot be stamped: a store header
// is returned as it is, and the card's pin (FieldTier) names its tier.
func tieredBrief(brief, tier string) string {
	lines := strings.Split(brief, "\n")
	for i, l := range lines {
		if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == "RESULT" {
			lines[i] = strings.TrimRight(l, " \t") + " tier: " + tier
			return strings.Join(lines, "\n")
		}
	}
	if len(lines) == 0 {
		return brief
	}
	if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(lines[0])); ok && storeHeader[k] {
		return brief
	}
	lines[0] = strings.TrimRight(lines[0], " \t") + " tier: " + tier
	return strings.Join(lines, "\n")
}

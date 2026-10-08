package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The tier a card is admitted with is written on the card's RESULT: line, the line the card
// contract carries it on (docs/SPEC-CARD-CONTRACT.md section 6). A card header (REPO:, BASE:,
// STATUS:, PATHS:, TEST:, ...) and a card's title are read as written by the staging, the
// frame and the card's own readers, so the tier word is never appended to either
// (docs/SPEC-SPRINT.md, the card decides its model). A brief with no RESULT: line carries no
// line to stamp; the card's own tier field names its tier and the add persists it
// (tier_model.go, steps_work.go).

// tieredBrief is brief with " tier: <t>" on its RESULT: line, the one line the card contract
// carries the tier on. A brief with no RESULT: line -- a header-first brief or a bare title --
// has no line the tier belongs on: it is returned as it is, and the card's own tier field
// names its tier (FieldTier, steps_work.go). A RESULT: line that already names a tier is
// never stamped a second word.
func tieredBrief(brief, tier string) string {
	lines := strings.Split(brief, "\n")
	for i, l := range lines {
		if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == "RESULT" {
			if strings.Contains(strings.ToLower(l), "tier:") {
				return brief // the RESULT line names a tier already
			}
			lines[i] = strings.TrimRight(l, " \t") + " tier: " + tier
			return strings.Join(lines, "\n")
		}
	}
	return brief
}

package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The tier a card is admitted with is written on the card's own line, the line the card
// contract carries it on (docs/SPEC-CARD-CONTRACT.md section 6): its RESULT: line when the
// brief has one, else line 1 when line 1 is the card's title. A card header (REPO:, BASE:,
// STATUS:, PATHS:, TEST:, ...) is read as written by the staging, the frame and the card's
// own readers, so the tier word is never appended to one (docs/SPEC-SPRINT.md, the card
// decides its model).

// cardHeaderKey is a card contract header key, upper-cased: the keys a card's readers read a
// header line by (docs/SPEC-CARD-CONTRACT.md, the header grammar). A line that carries one is
// a header, never the card's own line, so the tier is never stamped there.
var cardHeaderKey = map[string]bool{
	"STATUS": true, "REPO": true, "BASE": true, "BASE-REPO": true, "BASE-SHA": true,
	"RESULT": true, "KIND": true, "PATHS": true, "PATH": true, "TEST": true,
	"LEGS": true, "LEG": true, "SOURCE": true, "START": true, "STOP": true,
	"SHARED": true, "DEPENDS-ON": true, "NEEDS": true, "WHO": true, "PRIORITY": true,
	"BENCH": true, "NEW": true, "DONE-WHEN": true, "ATTRIBUTION": true, "WORK": true,
	"CARRY": true, "FROM": true, "MODEL": true, "TOKENS": true, "DEADLINE": true,
	"STAGE": true, "TIER": true, "PR-HEAD": true,
}

// cardOwnLine reports whether line is the card's own line -- the line the tier belongs on --
// rather than one of the header lines a card's readers read as written. A line that is no
// header is the card's title, and is stamped.
func cardOwnLine(line string) bool {
	k, _, ok := cardhdr.KeyValue(strings.TrimSpace(line))
	return !ok || !cardHeaderKey[strings.ToUpper(k)]
}

// tieredBrief is brief with " tier: <t>" on the card's own line: its RESULT: line when the
// brief has one, else line 1 when line 1 is the card's title (cardOwnLine). A brief whose
// first line is a header -- a store header (REPO:, BASE:, STATUS:) or a card header (PATHS:,
// TEST:, ...) -- carries no other line the tier belongs on: it is returned as it is, the
// missing tier is the card checks' to refuse, and the card's pin (FieldTier) names its tier.
func tieredBrief(brief, tier string) string {
	lines := strings.Split(brief, "\n")
	for i, l := range lines {
		if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == "RESULT" {
			lines[i] = strings.TrimRight(l, " \t") + " tier: " + tier
			return strings.Join(lines, "\n")
		}
	}
	if len(lines) == 0 || !cardOwnLine(lines[0]) {
		return brief
	}
	lines[0] = strings.TrimRight(lines[0], " \t") + " tier: " + tier
	return strings.Join(lines, "\n")
}

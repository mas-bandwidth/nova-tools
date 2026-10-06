package sprint

import (
	"fmt"
	"strings"
)

// minQuoted is the shortest finding crosswiredFix looks for in a fix: under it, a finding's
// words are too few to say the fix quotes it.
const minQuoted = 16

// crosswiredFix is why a rework of several cards is refused whole, "" when it is not: its
// one --fix quotes the reader finding of a card of the group and not the finding of another
// whose finding differs, so that other card's next attempt would be told to fix a defect
// not its own (docs/SPEC-SPRINT.md, rework; the card group-rework-keeps-each-finding, after
// 2026-10-05). A --fix that quotes no finding is every card's fix as written, and a rework
// with no --fix gives each card its own finding as its fix.
func crosswiredFix(s *Snapshot, chosen []*Card, fix string) string {
	if len(chosen) < 2 || strings.TrimSpace(fix) == "" {
		return ""
	}
	findings := make([]string, len(chosen))
	for i, c := range chosen {
		findings[i] = strings.TrimSpace(brokenFindings(s, c))
	}
	// the whole finding, or its first sentence (a quote cut short still names its defect)
	quotes := func(f string) bool {
		return len(f) >= minQuoted && strings.Contains(fix, f) || len(firstSentence(f)) >= minQuoted && strings.Contains(fix, firstSentence(f))
	}
	for i, f := range findings {
		if !quotes(f) {
			continue
		}
		for j, g := range findings {
			if g == f || quotes(g) {
				continue
			}
			own := "none"
			if g != "" {
				own = firstSentence(g)
			}
			return fmt.Sprintf("--fix is the reader finding of %s, not of %s (its own: %s); rework the group without --fix, and each card takes its own finding as its fix, or rework %s alone with --one",
				chosen[i].ID, chosen[j].ID, own, chosen[i].ID)
		}
	}
	return ""
}

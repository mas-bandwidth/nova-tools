package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSchedulingCostSpec175 pins the coordination requirement of nova-tools#175
// on docs/SPEC-WORK.md: measure the daily blended virtual cost per token and
// route eligible work inside the same quality, token, cost and shared-budget
// limits. The route selector is #175's (SPEC-WORK, *Delegation* / "What this
// section does not do"), and the future derived view is PROPOSAL-SCHEDULING-COST.md;
// this test keeps the requirement from silently dropping out of the spec the way
// the sibling recovery requirement of #187 is pinned. Package ci reads the repo as
// text, so a missing phrase is a red test and a phrase removed by a later edit is
// red again.
func TestSchedulingCostSpec175(t *testing.T) {
	// Whitespace is normalised so a phrase may wrap a source line the way the
	// rest of this spec wraps at the margin, without the wrap making the test red.
	spec := strings.Join(strings.Fields(readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-WORK.md"))), " ")

	required := []string{
		"blended virtual cost per token",
		"non-overlapping counted tokens",
		"priced coverage",
		"never summed as zero",
		"the highest-quality useful work with the fewest tokens",
		"the lowest average cost per token",
		"mandatory review",
		"extra rework",
		"stage-attribution gap",
		"versioned, configurable token-category weights",
		"dated monetary profiles",
		"Wall-clock time is priority #3",
		"urgency mode records",
		"shared account",
		"reserved for coordination, recovery and essential review",
		"Unknown remaining quota is not unlimited capacity",
		"significant-cost thresholds",
	}
	for _, want := range required {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md does not carry the #175 scheduling-cost requirement %q", want)
		}
	}
}

package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-SWARM.md, issue #856: a card is a pipeline of stateless model calls, not
// an agent loop. The section is the deliverable, so a rule renamed out of it is
// red here before any implementation is trusted. This reads the doc the way
// internal/decide's doc test reads SPEC-DECIDE.md.
func TestSpecSwarmNamesTheCardPipeline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("SPEC-SWARM.md is missing: %s", err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"## The card is a pipeline, not a loop (issue #856)",
		"a card is a pipeline of stateless model calls, not an agent loop",
		"One model call per step, and its input is exactly what the card names",
		"the harness runs the tools, with no model call",
		"no memory between calls",
		"`MODE: explore` is the one place the loop stays",
		"a fourth call without `MODE: explore` is refused, with the remedy line",
		"a `MODE: explore` card carries a turn budget the harness enforces",
		"the fix-card shape is three calls, not thirty turns",
		"Red tests for this section",
		"TestAdmissionRefusesAFourthCallWithoutExplore",
		"TestExploreOverTurnBudgetIsStoppedWithTheBudgetNamed",
		"TestTheFixCardRunsInThreeModelCalls",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-SWARM.md does not name the card-pipeline rule keyed by %q", phrase)
		}
	}
}

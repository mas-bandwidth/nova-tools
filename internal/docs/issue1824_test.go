package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestIssue1824 verifies that SPEC-PULSE.md rule 12 says the REPO and BRANCH
// lines on a RESULT.md are the worker's claim, not an instruction: the actual
// push destination is what the manager recorded (the launch record, or the
// coordinator's --clone when there is no launch record), and a RESULT claiming a different repository is
// refused. Before nova-tools #1824, harvest formed https://github.com/<REPO>.git
// from the worker's own line and pushed there with no check.
func TestIssue1824(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-PULSE.md")
	if err != nil {
		t.Fatalf("docs/SPEC-PULSE.md: %v", err)
	}
	content := collapseWS(string(spec))

	// Rule 12 must say the RESULT.md REPO/BRANCH lines are a worker's claim,
	// and the actual destination is checked against what the manager recorded.
	for _, want := range []string{
		"The `REPO` and `BRANCH` lines on `RESULT.md` are a worker's claim",
		"the actual push destination is the dispatch record",
		"the `REPO` line from `RESULT.md` is checked against the manager's recorded dispatch destination (the launch record, or the coordinator's `--clone` when there is no launch record)",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-PULSE.md rule 12 missing %q (nova-tools #1824)", want)
		}
	}

	// SPEC-SWARM 40-44 says "nowhere in the code", but the code does enforce
	// this now (harvestdest.go resolveDestination). The spec should note that.
	swarm, err := os.ReadFile("../../docs/SPEC-SWARM.md")
	if err != nil {
		t.Fatalf("docs/SPEC-SWARM.md: %v", err)
	}
	swarmContent := collapseWS(string(swarm))
	if strings.Contains(swarmContent, "nowhere in the code") {
		t.Errorf("docs/SPEC-SWARM.md:40-44 still says %q but the code enforces the rule now via resolveDestination/harvestdest.go (nova-tools #1824)",
			"nowhere in the code")
	}
}

var wsRe = regexp.MustCompile(`\s+`)

// collapseWS replaces every run of whitespace with a single space.
func collapseWS(s string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2233 pins nova-tools#2233: pull cards by atomic rename and enforce
// the capacity line as requests and the load1 gate. The spec's normative prose
// (Part 2) and its demands list (entries 15-17) name the behaviours, so Part 5
// — the red-tests section — must prove them: one puller take by atomic rename
// into taken/<worker>-<name>.card, the capacity line as Job requests, and the
// load1 decline. A red test the section does not name is not proven.
func TestIssue2233(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-FLEET-KUBE.md")
	if err != nil {
		t.Fatalf("docs/SPEC-FLEET-KUBE.md: %v", err)
	}
	content := string(body)
	flat := strings.ReplaceAll(content, "\n", " ") // spec lines wrap; phrases must hold across them

	// The issue's receipt: the quoted spec lines and the three demanded tests.
	for _, want := range []string{
		"nova-swarm pull --submit",
		"taken/<worker>-<name>.card",
		"Two pullers cannot take one card because the rename decides",
		"cpu: 1",
		"memory: 2Gi",
		"ephemeral-storage: 2Gi",
		"limits.memory: 2Gi",
		"25 GiB",
		"cores*1.5 - load1 <= 0",
		"nova-wake probe --here",
		"TestPullerTakesOneCardByAtomicRename",
		"TestJobRequestsAndLimitsAreTheCapacityLine",
		"TestPullerDeclinesBelowTheLoadLine",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("spec is missing the #2233 receipt text: %q", want)
		}
	}

	// The control that bites: Part 5 must carry a red test for the pull, the
	// capacity requests and the load gate — not just the demands list.
	start := strings.Index(content, "## Part 5")
	end := strings.Index(content, "## Tests this spec demands")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("spec is missing the Part 5 red-tests section or the demands section")
	}
	part5 := strings.ReplaceAll(content[start:end], "\n", " ")
	for _, want := range []string{
		"atomic rename",
		"taken/<worker>-<name>.card",
		"two pullers cannot take one card because the rename decides",
		"limits.memory: 2Gi",
		"25 GiB",
		"cores*1.5 - load1 <= 0",
	} {
		if !strings.Contains(part5, want) {
			t.Errorf("Part 5 names no red test proving #2233: %q", want)
		}
	}
}

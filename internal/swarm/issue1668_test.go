package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T24 (#1668): SPEC-TOOLWORK is the index. Each section's normative text moved
// home into the spec of the tool that holds it (SPEC-PULSE, SPEC-SWARM,
// SPEC-MERGE, SPEC-SANDBOX, SPEC-CI), the eligibility rule and the work list
// stay, and this file shrinks to the index. The test pins that outcome: the
// staying parts are still there, no section's normative body remains, and each
// home spec holds its section's text.
func TestIssue1668Repro(t *testing.T) {
	toolwork := readToolworkSpec(t)
	for _, want := range []string{"The eligibility rule", "The work list"} {
		if !strings.Contains(toolwork, want) {
			t.Errorf("SPEC-TOOLWORK is the index: %q must stay in it (#1668: the eligibility rule and the work list stay); got:\n%s", want, toolwork)
		}
	}
	// Every section 1-7 opened its normative body with these two paragraphs;
	// none remains in the index after the move.
	for _, gone := range []string{"**What those rules do not hold.**", "**Builds on.**"} {
		if strings.Contains(toolwork, gone) {
			t.Errorf("SPEC-TOOLWORK still carries a section's normative body (%q); #1668 moves each section's normative text home into its own spec and leaves the index", gone)
		}
	}
	// And each home spec now holds its section's text, by a phrase only that
	// section carries.
	homes := []struct {
		spec string
		want string
	}{
		{"SPEC-PULSE.md", "ACCEPT ABSTAIN"},                        // section 1, the accept gate
		{"SPEC-SANDBOX.md", "CERTIFY LEG"},                         // section 2, a wall that can build this repository
		{"SPEC-SWARM.md", "internal/hygiene.Check"},                // section 3, identity and hygiene
		{"SPEC-PULSE.md", "OUTCOME label="},                        // section 4, typed results
		{"SPEC-SWARM.md", "internal/pulse/kinds.go"},               // section 5, card kinds for tool work
		{"SPEC-MERGE.md", "no ACCEPT OK for head"},                 // section 6, lanes and landing
		{"SPEC-CI.md", "TestEveryTranscriptIsExecutedLineForLine"}, // section 7, tests that execute documents
	}
	for _, home := range homes {
		if !strings.Contains(readToolworkSpec(t, home.spec), home.want) {
			t.Errorf("%s does not hold %q; #1668 moves each SPEC-TOOLWORK section's normative text home into its own spec and leaves the index", home.spec, home.want)
		}
	}
}

// readToolworkSpec reads one document under docs/ the way this package's other
// spec tests do: relative to the package, on the working tree.
func readToolworkSpec(t *testing.T, names ...string) string {
	t.Helper()
	name := "SPEC-TOOLWORK.md"
	if len(names) > 0 {
		name = names[0]
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatalf("reading docs/%s: %v", name, err)
	}
	return string(raw)
}

package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2206 verifies that docs/SPEC-TEST.md documents the requirements
// for equivalent run reuse (nova-tools #2206). The spec must document that:
// "Implement nova-test `run` equivalence so only a fully equivalent prior run is reused"
// Equivalence includes source, dependencies, workflow/recipe revision, environment,
// policy, event/trust context and required coverage; matching SHA alone is insufficient.
func TestIssue2206(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-TEST.md")
	if err != nil {
		t.Fatalf("docs/SPEC-TEST.md: %v", err)
	}
	body := string(spec)

	// The spec must describe the `run` verb and its equivalence requirements.
	wants := []string{
		"finds an equivalent running or completed local or hosted run before",
		"dispatch and reuses it. Equivalence includes source, dependencies,",
		"workflow/recipe revision, environment, policy, event/trust context and",
		"required coverage; matching SHA alone is insufficient.",
	}

	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("docs/SPEC-TEST.md missing required concept: %q", want)
		}
	}
}

package docs

import (
	"os"
	"strings"
	"testing"
)

// TestNovaTestSpecFirstSlice pins the read-only first slice of the nova-test
// proposal (nova-tools #247): a versioned validation-manifest plan, run reuse
// by full equivalence (never SHA alone), and compact failure receipts. The
// spec file docs/SPEC-TEST.md is the deliverable; a missing file or a section
// missing one of the five verbs is a bug.
func TestNovaTestSpecFirstSlice(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-TEST.md")
	if err != nil {
		t.Fatalf("docs/SPEC-TEST.md: %v", err)
	}
	content := string(body)

	for _, want := range []string{
		"# nova-test",
		"`plan`",
		"`run`",
		"`status`",
		"`failures`",
		"`receipt`",
		"equivalence",
		"manifest",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-TEST.md missing %q", want)
		}
	}
}

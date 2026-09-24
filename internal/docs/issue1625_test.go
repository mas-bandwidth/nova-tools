package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue1625 pins the key aspect of nova-tools #1625: decide housekeeping:
// routing is a launcher step, not a line in a brief. The spec must explicitly
// state that routing is a launcher step, not optional, and not a line that can
// be forgotten in a brief.
func TestIssue1625(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-DECIDE.md")
	if err != nil {
		t.Fatalf("docs/SPEC-DECIDE.md: %v", err)
	}
	content := string(spec)

	// The key phrase that nova-tools #1625 asks for in the spec
	want := "decide housekeeping: routing is a launcher step, not a line in a brief"
	if !strings.Contains(content, want) {
		t.Errorf("docs/SPEC-DECIDE.md missing %q (nova-tools #1625)", want)
	}
}

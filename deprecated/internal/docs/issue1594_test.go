package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue1594 pins the decision nova-tools#1594 asks for: an `event --kind
// cancel` must settle the node into C with disposition `:cancelled`, not leave it
// in O. The kernel's `:terminal` branch that only sets `wnode-state` and writes no
// `:settle` disagrees with the rest of SPEC-WORK, which treats a cancelled item as
// closed. The text below is the spec sentence that resolves the discrepancy.
func TestIssue1594(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-WORK.md")
	if err != nil {
		t.Fatalf("read docs/SPEC-WORK.md: %v", err)
	}
	text := string(body)

	want := "An `event --kind cancel` settles the node into C with disposition `:cancelled`; it does not leave the node in O"
	collapsed := strings.Join(strings.Fields(text), " ")
	if !strings.Contains(collapsed, want) {
		t.Errorf("docs/SPEC-WORK.md is missing the #1594 decision sentence %q; a cancelled item must be closed in the spec, and `event --kind cancel` must not leave the node in O", want)
	}
}

package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue1661 proves that docs/SPEC-TOOLWORK.md describes what nova-tools #1661
// asks for: toolwork T17 (builder + friends first) for nova-merge batch — admit
// a swarm's member only with its ACCEPT OK, run hygiene on every member, and
// name the member that breaks the build. #1661 is open and the spec must
// already carry the three rules named in the issue body and the two red-test
// names that the issue asks to be red first.
func TestIssue1661(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-TOOLWORK.md")
	if err != nil {
		t.Fatalf("docs/SPEC-TOOLWORK.md: %v", err)
	}
	content := string(body)

	wants := []string{
		// T17 is on the work list, named for issue #1661.
		"T17 (#1661)",
		// §6 rule 7 admits a member only with its ACCEPT OK whose head= is the
		// member's current head and whose control= is on file.
		"`ACCEPT OK` whose `head=` is the member's current head",
		"whose `control=` is on file",
		// The §6 rule 7 path for a member without an ACCEPT OK.
		"BATCH DROP",
		"no ACCEPT OK for head",
		// Hygiene runs over every member whatever its origin, and the spec points
		// at §3 rule 7's identities.tsv and out-of-path behaviour.
		"`batch` runs `internal/hygiene.Check`",
		"identities.tsv",
		"out-of-path` is skipped for it",
		// The merged tree must compile, with #1479 / useFakeForge as the reason,
		// and the failing member is named by bisecting once.
		"merged tree must compile",
		"undefined: useFakeForge",
		"bisecting the\n   members once",
		// The two red-test names #1661 names first.
		"batch-names-the-member-that-breaks-the-build",
		"swarm-member-without-accept-ok-is-dropped",
	}
	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Errorf("docs/SPEC-TOOLWORK.md missing %q (nova-tools #1661, toolwork T17)", w)
		}
	}
}

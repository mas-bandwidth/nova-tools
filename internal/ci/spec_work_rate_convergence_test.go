package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkCarriesTheRateAndConvergenceRules pins that docs/SPEC-WORK.md states the
// rate-and-convergence rules of 2026-09-15 as they apply to nova-work, the resident-session
// half of the note Glenn asked be applied "to future tool specs, nova work etc." (#553): the
// resident session runs the same tick and pool floor over the tree, check carries contraction
// from the journal, the scope gate is a node property, and bug nodes close only by test.
func TestSpecWorkCarriesTheRateAndConvergenceRules(t *testing.T) {
	root := repoRoot(t)
	spec := readFile(t, filepath.Join(root, "docs", "SPEC-WORK.md"))

	for _, want := range []string{
		"the resident session runs the same tick and pool floor over the tree",
		"check carries contraction from the journal",
		"the scope gate is a node property",
		"bug nodes close only by test",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md does not carry the rate-and-convergence rule %q; the resident session's rules are named in #553", want)
		}
	}
}

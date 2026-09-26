package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// docs/TESTS.md must state, in its own words, what a first-run transcript owes
// a reader about a step the machine running it cannot meet: the `Requires:`
// line, and the `SKIP-PRECONDITION` a harness reports rather than a defect. The
// point is the one the issue is filed for (#1549) -- a card bench must not hold
// a JEV key, a forge credential or a posting credential, and a harness with no
// word for a step that needs one records the step as DEFECT, which is simply
// false.
//
// This guards the document rather than the harness: `Requires:`/`Platform:` are
// read and `SKIP-PRECONDITION` is emitted by internal/onboarding, and the
// file's header is the one place a reader -- and a harness told to grade from
// this file -- learns that a step skipped for a reason the file does not state
// is a defect in the file, not a pass.
func TestTESTSmdStatesThePreconditionConvention(t *testing.T) {
	t.Parallel()

	md := readFile(t, filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	if !strings.Contains(md, "Requires:") {
		t.Errorf("docs/TESTS.md never states `Requires:`; a section owes a reader one line per precondition the machine may not have, exactly as `Platform:` already does")
	}
	if !strings.Contains(md, "SKIP-PRECONDITION") {
		t.Errorf("docs/TESTS.md never states `SKIP-PRECONDITION`; a harness that cannot meet a stated precondition has no word for it, and records the step as a defect")
	}
}

package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkWorkerWideningUnambiguous pins that docs/SPEC-WORK.md states the
// worker-widening rule in one way only: a worker past its effort limit stops and
// reports, and widening a card's :effort is the coordinator's act alone. The
// former phrasing "rather than widening on its own" read two ways (a worker
// might widen with help), contradicting "only the coordinator may widen".
func TestSpecWorkWorkerWideningUnambiguous(t *testing.T) {
	root := repoRoot(t)
	spec := readFile(t, filepath.Join(root, "docs", "SPEC-WORK.md"))

	const ambiguous = "rather than widening on its own"
	if strings.Contains(spec, ambiguous) {
		t.Errorf("docs/SPEC-WORK.md worker-widening rule reads two ways: it still contains %q; a worker past its effort limit must stop and report, and only the coordinator widens a card's :effort", ambiguous)
	}
}

package shrinkonly

import (
	"path"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
)

// ShrinkOnlyOutsideTestdata are the shrink-only ledgers that live outside
// diffcheck.LedgerDir: the unit tier's two debt lists, which make test hands nova-ci
// slowtests (internal/ci/unit_tier_class_test.go). Each only loses rows.
var ShrinkOnlyOutsideTestdata = []string{
	"internal/ci/sleeps-skips_allowlist.txt",
	"internal/ci/slow-tests_allowlist.txt",
}

// growingLists are the top-level lists under diffcheck.LedgerDir that grow: the
// deleted-tests log is appended to (classtests_class_test.go, RepeatedKeys), and two lists
// gain a row by hand, with no base or ceiling to refuse it: compared_examples.txt names
// the test that executes a pasted example (ci_platforms_and_examples_test.go: an example
// leaves unexecuted_examples.txt by being named here), and namedpaths_allowlist.txt
// holds every name that looks like a path and is not one (namedpaths_class_test.go: a
// new such name is a red run until it is listed).
var growingLists = []string{"deleted-tests.txt", "compared_examples.txt", "namedpaths_allowlist.txt"}

// DeadCode is the dead code shrink-only allowlist (docs/SPEC-CI.md), the one counted
// ledger in internal/ci today.
const DeadCode = "internal/ci/testdata/dead_code_allowlist.txt"

// ShrinkOnly says p (a path from the repository root, slash-separated) is a
// shrink-only ledger: one whose class test says it only shrinks, so a change to it is a
// removal of rows (and, on a list with a `# ceiling: N` line, a lowering of N), never an
// addition. These are every top-level list file under diffcheck.LedgerDir (the lists
// internal/ci loads with Options.Ceiling: allowlist_update_test.go, shrinkOnly, and the
// class tests' own ceiling options) except the lists that grow (growingLists), and
// ShrinkOnlyOutsideTestdata. The counted-ledger shards below LedgerDir's directories are
// not: their rows carry counts that are lowered in place. The lander resolves a merge
// conflict in a shrink-only ledger as the union of both sides' removals
// (docs/SPEC-SPRINT.md section 7; the lander is nova-sprint's, in its own repository); this is the one
// place that says which ledgers those are.
func ShrinkOnly(p string) bool {
	for _, q := range ShrinkOnlyOutsideTestdata {
		if p == q {
			return true
		}
	}
	rest, ok := strings.CutPrefix(p, diffcheck.LedgerDir)
	if !ok || strings.Contains(rest, "/") || !diffcheck.Ledger(p) {
		return false
	}
	for _, g := range growingLists {
		if path.Base(rest) == g {
			return false
		}
	}
	return true
}

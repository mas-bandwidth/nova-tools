package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/diffcheck"
)

// landScope is E12 on the rig's merge as checkCard reads it (cmd/nova-sprint/land.go):
// the merge's diff from the base, and the files the base tracks.
func (r *mergeRig) landScope(paths []string) (diff string, tracked []string, amended, refused []string) {
	r.t.Helper()
	diff = r.must("diff", "-M", "--no-color", r.before, "HEAD")
	tracked = strings.Split(r.must("ls-tree", "-r", "--name-only", r.before), "\n")
	amended, refused = sprint.LandScope(paths, diff, tracked)
	return diff, tracked, amended, refused
}

// The files a change must touch to keep the tree green are inside every card's PATHS
// (cardgen.AlwaysInPathsRule; docs/SPEC-SPRINT.md section 7, the lander's checks): a
// head that changes its PATHS file and a test, a testdata fixture, both TLA+ ledgers, the
// docs catalog and an AGENTS.md map its PATHS do not name passes E12, and without the
// rule the same diff is refused for each (the reversed witness); a non-test source file
// outside PATHS, and a source file renamed to a test's name, are still refused.
func TestE12NeverRefusesATestOrALedger(t *testing.T) {
	t.Parallel()
	paths := []string{"internal/a/*.go"}
	base := map[string]string{
		"internal/a/a.go":                    "package a\n",
		"internal/b/b.go":                    "package b\n",
		"internal/b/b_test.go":               "package b\n",
		"internal/b/testdata/golden.txt":     "old\n",
		"tla/RUNS.tsv":                       "module\tresult\n",
		"tla/CASES.tsv":                      "case\tmodule\n",
		"internal/docs/catalog.go":           "package docs\n\nvar x = E(\"internal/b\")\n",
		"AGENTS.md":                          "# map\n",
		"internal/b/AGENTS.md":               "# b\n",
		"internal/ci/testdata/allowlist.txt": "row\n",
	}
	green := map[string]string{
		"internal/a/a.go":                    "package a\n\nfunc A() {}\n",
		"internal/b/b_test.go":               "package b\n\n// changed\n",
		"internal/b/new_test.go":             "package b\n",
		"internal/b/testdata/golden.txt":     "new\n",
		"internal/b/testdata/deep/case.txt":  "case\n",
		"tla/RUNS.tsv":                       "module\tresult\nA\tok\n",
		"tla/CASES.tsv":                      "case\tmodule\nc\tA\n",
		"internal/docs/catalog.go":           "package docs\n\nvar x = E(\"internal/b\", \"edited\")\n",
		"AGENTS.md":                          "# map\n\nedited\n",
		"internal/b/AGENTS.md":               "# b\n\nedited\n",
		"internal/ci/testdata/allowlist.txt": "row\nrow2\n",
	}
	byRule := []string{
		"AGENTS.md", "internal/b/AGENTS.md", "internal/b/b_test.go", "internal/b/new_test.go",
		"internal/b/testdata/deep/case.txt", "internal/b/testdata/golden.txt", "internal/docs/catalog.go",
		"tla/CASES.tsv", "tla/RUNS.tsv",
	}

	t.Run("a test and the ledgers outside PATHS pass E12", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, base, green)
		diff, tracked, amended, refused := r.landScope(paths)
		assert.Empty(t, refused, "E12 refuses none of them")
		assert.Empty(t, amended, "inside PATHS by rule is not a scope amendment")
		// the reversed witness: the same merge, the rule left out, is refused for each
		_, without := sprint.ScopeAmended(nil, diffcheck.Outside(paths, diff, tracked))
		assert.ElementsMatch(t, byRule, without)
	})
	t.Run("a test outside PATHS with nothing of its own passes E12", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, base, map[string]string{"internal/b/b_test.go": "package b\n\n// only\n"})
		_, _, amended, refused := r.landScope(paths)
		assert.Empty(t, refused)
		assert.Empty(t, amended)
	})
	t.Run("a non-test source file outside PATHS is still refused", func(t *testing.T) {
		t.Parallel()
		head := map[string]string{"internal/b/b.go": "package b\n\nfunc B() {}\n"}
		for f, s := range green {
			head[f] = s
		}
		r := newMergeRig(t, base, head)
		_, _, _, refused := r.landScope(paths)
		assert.Equal(t, []string{"internal/b/b.go"}, refused)
	})
	t.Run("a source file renamed to a test's name is still refused", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, base, map[string]string{"internal/a/a.go": "package a\n\nfunc A() {}\n"})
		r.must("mv", "internal/b/b.go", "internal/b/moved_test.go")
		r.must("commit", "-q", "-m", "move")
		_, _, _, refused := r.landScope(paths)
		assert.Equal(t, []string{"internal/b/moved_test.go"}, refused)
	})
	t.Run("a source rename cannot borrow a changed package's test amendment", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, base, map[string]string{"internal/a/a.go": "package a\n\nfunc A() {}\n"})
		r.must("mv", "internal/b/b.go", "internal/a/moved_test.go")
		r.must("commit", "-q", "-m", "move")
		_, _, amended, refused := r.landScope(paths)
		assert.Empty(t, amended)
		assert.Equal(t, []string{"internal/a/moved_test.go"}, refused)
	})
}

// cardgen.AlwaysInPaths is the rule's one list: what it holds inside, and the near
// misses it leaves outside.
func TestAlwaysInPathsNamesTheFilesThatKeepTheTreeGreen(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		"a_test.go", "internal/x/y_test.go", "./internal/x/y_test.go", "cmd/z/testdata/a/b.json",
		"testdata/x.txt", "tla/RUNS.tsv", "tla/CASES.tsv", "internal/docs/catalog.go", "AGENTS.md", "cmd/AGENTS.md",
	} {
		assert.True(t, cardgen.AlwaysInPaths(p), p)
	}
	for _, p := range []string{
		"internal/x/y.go", "internal/x/test.go", "internal/x/testdata.go", "tla/COVERAGE.tsv",
		"tla/Sprint.tla", "internal/docs/docs.go", "docs/STANDARD.md", "AGENTS.md.bak", "../x_test.go", "/abs/x_test.go", "",
	} {
		assert.False(t, cardgen.AlwaysInPaths(p), p)
	}
}

package ci

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// selectConsumedByProcessSubstitution is the #4370 shape: the selection's
// answer read through `< <(...)`, which throws its exit status away.
var selectConsumedByProcessSubstitution = regexp.MustCompile(`<\s*<\(\s*go run \./tools/ci (select-packages|test-matrix)`)

// TestCIReadsSelectionExitStatus pins the caller: the workflow runs the verb as
// the step itself, so its failure stops the step; the verb fails on
// pull_request and falls back to the whole tree elsewhere.
func TestCIReadsSelectionExitStatus(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	assert.False(t, selectConsumedByProcessSubstitution.MatchString(src), "ci.yml reads the selection through `< <(...)`, which discards its exit status: a go list failure became `test (nothing)` on PR #4370; run the verb as the step")
	block := jobBody(src, "test-packages")
	assert.Contains(t, block, ciRunner+" test-matrix --event", "test-packages does not run `ci test-matrix` as the step itself, so its exit status is the step's")
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_matrix.go"))
	for _, want := range []string{
		`WholeTreeOnError: *event != "pull_request"`,
		`select-packages failed; see its error above`,
	} {
		assert.Containsf(t, verb, want, "tools/ci/sel_matrix.go does not contain %q: pull_request fails the job on a go list failure, merge_group and push fall back to the whole tree", want)
	}
	assert.Contains(t, verb, "return 1", "tools/ci/sel_matrix.go never exits 1 on a failed selection")
}

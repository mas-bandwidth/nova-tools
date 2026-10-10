package main

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyNamesTheIssueCountOfEachSide pins the VERIFY line's issue counts
// to the side they count: the tree file's `<tree_issues>` and the other
// side's `<against_issues>`, which is GitHub's tree or the --against file
// (SPEC-WORK-V1 section 1.6, the comparison's two sides).
func TestVerifyNamesTheIssueCountOfEachSide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.lisp"), filepath.Join(dir, "b.lisp")
	res := workMain(recorded(t, "/bin/gh")).Run("import", "--org", "mas-bandwidth",
		"--repo", "mas-bandwidth/reliable", "--page-size", "15", "--out", a)
	require.Equal(t, 0, res.Code, "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	testkit.WriteFile(t, b, minimalTree)

	res = workMain(unreachable(t)).Run("verify", "--tree", a, "--against", b)
	said := res.Stdout + res.Stderr
	assert.Contains(t, said, "tree_issues=20", "the tree file's issue count is not named:\n%s", said)
	assert.Contains(t, said, "against_issues=0", "the --against side's issue count is not named:\n%s", said)
}

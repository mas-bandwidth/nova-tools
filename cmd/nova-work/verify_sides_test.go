package main

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
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
	res := workMain(recorded(t, "/bin/gh")).Run("import", "--org", "example-org",
		"--repo", "example-org/reliable", "--page-size", "15", "--out", a)
	require.Equal(t, 0, res.Code, "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	testkit.WriteFile(t, b, minimalTree)

	res = workMain(unreachable(t)).Run("verify", "--tree", a, "--against", b)
	said := res.Stdout + res.Stderr
	assert.Contains(t, said, "tree_issues=20", "the tree file's issue count is not named:\n%s", said)
	assert.Contains(t, said, "against_issues=0", "the --against side's issue count is not named:\n%s", said)
}

// TestVerifyDefaultsToTheTreesImportScope pins the default scope verify
// compares (SPEC-WORK-V1 sections 1.2 and 1.6, tla/WorkImport.tla's Verify,
// which reads only the scope it imported): a tree imported with --repo
// records its scope in :scope, and verify with no --repo compares only that
// scope. A whole-organization --against tree holding another repository must
// therefore not be MISSING for it.
func TestVerifyDefaultsToTheTreesImportScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scoped, whole := filepath.Join(dir, "scoped.lisp"), filepath.Join(dir, "whole.lisp")
	res := workMain(unreachable(t)).Run("import", "--org", "example-org",
		"--repo", "example-org/reliable", "--page-size", "15", "--fixture", recording, "--out", scoped)
	require.Equal(t, 0, res.Code, "import exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)

	tree, err := workfile.Decode(scoped, []byte(testkit.ReadFile(t, scoped)), workfile.Limits(1<<20))
	require.NoError(t, err, "the scoped tree does not read back")
	// The whole-organization tree holds the same repository and one more;
	// it is built without any scope word, as a whole-organization tree is.
	all := &workfile.Tree{Source: tree.Source, Org: tree.Org, Fetched: tree.Fetched,
		Repos: append([]workfile.Repo{{Name: "example-org/other", URL: workfile.Web + "example-org/other"}}, tree.Repos...)}
	data, err := workfile.Encode(all)
	require.NoError(t, err)
	testkit.WriteFile(t, whole, string(data))

	res = workMain(unreachable(t)).Run("verify", "--tree", scoped, "--against", whole)
	said := res.Stdout + res.Stderr
	require.Equal(t, 0, res.Code, "the scoped tree fails against the whole organization; exit %d\n%s", res.Code, said)
	assert.NotContains(t, said, "MISSING path=repos/example-org/other", said)
}

// TestVerifyRefusesARepoOutsideTheTreesScopeBeforeGitHub pins SPEC-WORK-V1
// section 1.6: --repo narrows within the tree's scope, and one outside it is
// refused naming the scope, before any GitHub call.
func TestVerifyRefusesARepoOutsideTheTreesScopeBeforeGitHub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scoped := filepath.Join(dir, "scoped.lisp")
	res := workMain(unreachable(t)).Run("import", "--org", "example-org",
		"--repo", "example-org/reliable", "--page-size", "15", "--fixture", recording, "--out", scoped)
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)

	g, calls := countingGitHub()
	res = workMain(g).Run("verify", "--tree", scoped, "--repo", "example-org/other")
	said := res.Stdout + res.Stderr
	require.Equal(t, 2, res.Code, said)
	assert.Contains(t, said, "not in the tree's scope", said)
	assert.Contains(t, said, "example-org/reliable", said)
	assert.EqualValues(t, int64(0), calls.Load(), "GitHub was called before the scope refusal")
}

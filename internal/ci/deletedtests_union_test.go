package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// TestDeletedTestsLogMergesByUnionAndReadsUnordered holds the shape of the
// deleted-tests log (the classtests rule, docs/SPEC-CI.md): an unordered set of
// `<path> <why>` rows, so two changes that each append a row merge without a
// conflict (the .gitattributes line), and a union merge that leaves a row
// twice, or in any order, reads the same as one row. The merge is run in a
// repository built here, with the attribute line this repository checks in.
func TestDeletedTestsLogMergesByUnionAndReadsUnordered(t *testing.T) {
	t.Parallel()

	attrs, err := os.ReadFile(filepath.Join(repoTree(t).Root, ".gitattributes"))
	require.NoError(t, err)
	want := deletedTestsLogPath + " merge=union"
	var line string
	for _, l := range strings.Split(string(attrs), "\n") {
		if strings.TrimSpace(l) == want {
			line = l
		}
	}
	require.NotEmpty(t, line, ".gitattributes has no line %q: two changes that each delete a test file would conflict on the log", want)

	r := newScratchRepo(t, "main")
	r.write(".gitattributes", line+"\n")
	r.write(deletedTestsLogPath, "# the log\nold_test.go moved\n")
	r.stage("base")
	r.git("checkout", "-q", "-b", "one")
	r.write(deletedTestsLogPath, "# the log\nold_test.go moved\na_test.go gone one\nshared_test.go gone both\n")
	r.stage("one")
	r.git("checkout", "-q", "main")
	r.git("checkout", "-q", "-b", "two")
	r.write(deletedTestsLogPath, "# the log\nold_test.go moved\nb_test.go gone two\nshared_test.go gone both\n")
	r.stage("two")
	r.git("merge", "-q", "--no-edit", "one")
	merged, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(deletedTestsLogPath)))
	require.NoError(t, err)
	got, err := allowlist.Parse(deletedTestsLogPath, string(merged), allowlist.Options{})
	require.NoError(t, err)
	for _, key := range []string{"old_test.go", "a_test.go", "b_test.go", "shared_test.go"} {
		assert.True(t, got.Has(key), "the union merge lost the row for %s:\n%s", key, merged)
	}

	// Order and repetition change nothing the rule reads.
	scrambled := "shared_test.go gone both\nb_test.go gone two\nshared_test.go gone both\n# a comment\nold_test.go moved\na_test.go gone one\nold_test.go moved\n"
	list, err := allowlist.Parse(deletedTestsLogPath, scrambled, allowlist.Options{})
	require.NoError(t, err)
	assert.Len(t, distinctRows(list.Rows()), 4, "a scrambled log with repeated rows reads as its four distinct rows")
	diff := "--- a/" + deletedTestsLogPath + "\n+++ b/" + deletedTestsLogPath + "\n@@ -1,1 +1,5 @@\n+shared_test.go gone both\n+b_test.go gone two\n+shared_test.go gone both\n+a_test.go gone one\n"
	assert.Equal(t, map[string]string{
		"shared_test.go": "gone both", "b_test.go": "gone two", "a_test.go": "gone one",
	}, declaredRowsAdded(diff), "a repeated, unordered diff declares each row once")
}

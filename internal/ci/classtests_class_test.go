package ci

import (
	"context"
	"os/exec"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// classTestsListPath is the grow-only list of every class test file under
// internal/ci, one repo-relative path per row. A class test is a rule the
// repo keeps by structure; a merge that drops one drops the rule with it,
// and nothing else in the tree notices.
const classTestsListPath = "testdata/class-tests.txt"

// classTestsListOptions: the list grows (an update appends a new class
// test's row) and never carries a ceiling.
var classTestsListOptions = allowlist.Options{}

// classTestFile reports whether rel is a class test of this package.
func classTestFile(rel string) bool {
	return path.Dir(rel) == "internal/ci" && strings.HasSuffix(rel, "_class_test.go")
}

// TestNoMergeDeletesAClassTest is the class rule of 2026-09-26: #4346
// (rowan/functional-tag) was built on a base older than #4344 and its squash
// put a tree on dev that lacked the four files #4344 had added, the silent
// class test among them; every check was green, because a rule that is not
// there cannot fail. The list of class tests is checked in and only grows:
// every row's file must be in the tree, and a file the tree lacks is a red
// run that names the commit which deleted it. The list is never rewritten
// while a row is missing, so no update can drop the row instead of the
// finding.
func TestNoMergeDeletesAClassTest(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	list := loadAllowlist(t, classTestsListPath, classTestsListOptions)
	measured := map[string]bool{}
	for _, f := range tree.Files {
		if f.Test && classTestFile(f.Rel) {
			measured[f.Rel] = true
		}
	}
	if len(measured) == 0 {
		t.Fatal("no internal/ci/*_class_test.go in the tree; the walk is broken, not the tree")
	}

	var missing []string
	for _, row := range list.Rows() {
		if !measured[row.Key] {
			missing = append(missing, row.Key)
		}
	}
	sort.Strings(missing)
	for _, rel := range missing {
		t.Errorf("%s lists %s and the tree lacks it: %s. A merge never deletes a class test; "+
			"restore the file from the commit before that one (`git checkout <parent> -- %s`). "+
			"The list only grows, and no update drops this row",
			classTestsListPath, rel, classTestDeletedBy(tree.Root, rel), rel)
	}
	if len(missing) > 0 {
		// Never hand the helper a list with a missing row: under
		// NOVA_CI_UPDATE=1 it would drop the row, and the deletion with it.
		return
	}
	for _, rel := range allowlist.Check(t, list, measured).Unlisted {
		t.Errorf("%s is a class test %s does not list; add its row (NOVA_CI_UPDATE=1 does; the list only grows)",
			rel, classTestsListPath)
	}
}

// classTestDeletedBy names the commit on the current branch's first-parent
// line that removed rel, or says the history that holds it was not fetched.
func classTestDeletedBy(root, rel string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", "--first-parent", "-m",
		"--diff-filter=D", "-1", "--format=%h %s", "--", rel).Output()
	if s := strings.TrimSpace(string(out)); err == nil && s != "" {
		return "deleted by " + s
	}
	return "the deleting commit is not in the fetched history; `git log --first-parent -m --diff-filter=D -- " +
		rel + "` on a full clone names it"
}

// TestClassTestFileReadsThePath pins the shape: this package's *_class_test.go
// only, never a class test elsewhere or a plain test here.
func TestClassTestFileReadsThePath(t *testing.T) {
	t.Parallel()
	for rel, want := range map[string]bool{
		"internal/ci/silent_class_test.go":            true,
		"internal/ci/classtests_class_test.go":        true,
		"internal/ci/allowlist_update_test.go":        false,
		"internal/ci/allowlist/allowlist_test.go":     false,
		"internal/nsprint/land/guard/x_class_test.go": false,
		"internal/ci/testdata/x_class_test.go":        false,
	} {
		if got := classTestFile(rel); got != want {
			t.Errorf("classTestFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

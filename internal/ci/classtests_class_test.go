package ci

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// deletedTestsLogPath is where a change declares a test file or a list it
// means to delete: `<path> <why>` rows, added in the same change as the
// deletion. The rule reads the declaration out of git, never out of the
// tree: only a row this commit ADDS declares a deletion this commit makes.
const deletedTestsLogPath = "internal/ci/testdata/deleted-tests.txt"

// guardedByMergeRule reports whether rel is a file no merge may delete
// undeclared: every _test.go, and every list under internal/ci/testdata.
func guardedByMergeRule(rel string) bool {
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	return path.Dir(rel) == "internal/ci/testdata" && strings.HasSuffix(rel, ".txt")
}

// mergeDeletions is what one commit removed from its first parent's tree,
// and what it declared. For a pull request's merge ref and a merge-queue
// commit the first parent is the base branch's tip, so Deleted is exactly
// what merging the change takes away from dev; for a squash on dev it is
// the squash's effect; for a plain commit, that commit's.
type mergeDeletions struct {
	Head, Parent, Subject string
	// Deleted is every path gone from the tree, renames excluded (-M).
	Deleted []string
	// Declared is path -> why for the rows this commit added to the log.
	Declared map[string]string
}

// readMergeDeletions compares HEAD's tree with its first parent's, through
// the package's gitOut (issue2218.go: a bounded git in root).
func readMergeDeletions(root string) (*mergeDeletions, error) {
	raw, err := gitOut(root, "cat-file", "-p", "HEAD")
	if err != nil {
		return nil, err
	}
	// The raw object, not `log --format=%P`: a shallow checkout grafts the
	// parents away in traversal and keeps them in the object.
	var parents []string
	for _, line := range strings.Split(raw, "\n") {
		if p, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, p)
		}
		if line == "" {
			break
		}
	}
	if len(parents) == 0 {
		return nil, fmt.Errorf("HEAD has no parent: there is no merge to compare")
	}
	parent := parents[0]
	if _, err := gitOut(root, "cat-file", "-e", parent+"^{commit}"); err != nil {
		return nil, fmt.Errorf("HEAD's first parent %s is not in this checkout (a depth-1 fetch), so what the merge deleted cannot be read; every workflow checks out with fetch-depth: 2 for this rule", parent[:9])
	}
	head, err := gitOut(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return nil, err
	}
	subject, err := gitOut(root, "show", "-s", "--format=%s", "HEAD")
	if err != nil {
		return nil, err
	}
	m := &mergeDeletions{Head: strings.TrimSpace(head), Parent: parent[:9], Subject: strings.TrimSpace(subject), Declared: map[string]string{}}
	gone, err := gitOut(root, "diff", "-M", "--diff-filter=D", "--name-only", parent, "HEAD")
	if err != nil {
		return nil, err
	}
	for _, rel := range strings.Split(gone, "\n") {
		if rel = strings.TrimSpace(rel); rel != "" {
			m.Deleted = append(m.Deleted, rel)
		}
	}
	sort.Strings(m.Deleted)
	diff, err := gitOut(root, "diff", parent, "HEAD", "--", deletedTestsLogPath)
	if err != nil {
		return nil, err
	}
	for p, why := range declaredRowsAdded(diff) {
		m.Declared[p] = why
	}
	return m, nil
}

// declaredRowsAdded reads the `<path> <why>` rows a unified diff of the log
// adds: the declarations this change makes and no other.
func declaredRowsAdded(diff string) map[string]string {
	rows := map[string]string{}
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		row := strings.TrimSpace(line[1:])
		if row == "" || strings.HasPrefix(row, "#") {
			continue
		}
		p, why, _ := strings.Cut(row, " ")
		rows[p] = strings.TrimSpace(why)
	}
	return rows
}

// findings are the rule's red lines: a guarded file deleted with no row of
// this change declaring it, and a row declaring a deletion this change does
// not make.
func (m *mergeDeletions) findings() []string {
	deleted := map[string]bool{}
	for _, rel := range m.Deleted {
		deleted[rel] = true
	}
	var out []string
	for _, rel := range m.Deleted {
		if !guardedByMergeRule(rel) {
			continue
		}
		if _, ok := m.Declared[rel]; ok {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s) deletes %s, which its first parent %s had, and no row of %s added in the same change declares it: "+
			"restore the file (`git checkout %s -- %s`), or, if the deletion is meant, add the row `%s <why>` to %s in this change",
			m.Head, m.Subject, rel, m.Parent, deletedTestsLogPath, m.Parent, rel, rel, deletedTestsLogPath))
	}
	for rel, why := range m.Declared {
		if why == "" {
			out = append(out, fmt.Sprintf("%s adds the row %q to %s with no why; a declaration says why the file goes", m.Head, rel, deletedTestsLogPath))
		}
		if !deleted[rel] {
			out = append(out, fmt.Sprintf("%s adds the row %q to %s, but the change deletes no such file; a row declares a deletion the same change makes (drop the row)", m.Head, rel, deletedTestsLogPath))
		}
	}
	sort.Strings(out)
	return out
}

// THE CLASS TEST. What HEAD's merge took away from its first parent's tree
// is read from git and compared with what the same change declared, so a
// squash from a stale base — a tree that lacks files dev had, with nothing
// in the change saying so — is red on the pull request, in the merge queue
// and on dev. 2026-09-26: #4346 (rowan/functional-tag) was rebased onto
// #4344 with a tree that lacked the four files #4344 had added (the silent
// class test, its allowlist, two of its controls) and undid #4344's
// live-path fixes with them; every check was green, because a rule that is
// not there cannot fail, and a list of rules kept in the tree goes with the
// tree.
func TestNoMergeDeletesATestFileUndeclared(t *testing.T) {
	t.Parallel()

	log := loadAllowlist(t, "testdata/deleted-tests.txt", allowlist.Options{})
	for _, row := range log.Rows() {
		if _, why, _ := strings.Cut(row.Text, " "); strings.TrimSpace(why) == "" {
			t.Errorf("%s: %q carries no why", deletedTestsLogPath, row.Text)
		}
	}
	m, err := readMergeDeletions(repoTree(t).Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.findings() {
		t.Error(f)
	}
}

// TestMergeRuleReadsTheDeletionOutOfGit proves the rule over a repository
// it builds: the stale-base squash shape is red for the test file and the
// list and silent for a plain source file; a rename is not a deletion; a
// row added in the same change declares a deletion, and a row that names
// no deletion is red.
func TestMergeRuleReadsTheDeletionOutOfGit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if _, err := gitOut(root, append([]string{
			"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(rel string) {
		t.Helper()
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	findings := func() []string {
		t.Helper()
		m, err := readMergeDeletions(root)
		if err != nil {
			t.Fatal(err)
		}
		return m.findings()
	}

	git("init", "-q")
	write("a/x_test.go", "package a\n")
	write("internal/ci/testdata/foo_allowlist.txt", "# a list\n")
	write(deletedTestsLogPath, "# the log\n")
	write("b/keep_test.go", strings.Repeat("package b\n\n// a test file with enough body to be recognised across a rename\n", 4))
	write("c/main.go", "package c\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	if _, err := readMergeDeletions(root); err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Fatalf("a root commit: err = %v; want no parent", err)
	}

	// The stale-base squash shape: files the parent had are gone, nothing
	// in the change says so. The test file and the list are red; the
	// source file is not this rule's.
	remove("a/x_test.go")
	remove("internal/ci/testdata/foo_allowlist.txt")
	remove("c/main.go")
	git("add", "-A")
	git("commit", "-q", "-m", "squash from a stale base")
	got := findings()
	if len(got) != 2 || !strings.Contains(got[0], "deletes a/x_test.go") || !strings.Contains(got[1], "deletes internal/ci/testdata/foo_allowlist.txt") ||
		!strings.Contains(got[0], "squash from a stale base") || !strings.Contains(got[0], "git checkout ") {
		t.Fatalf("stale-base squash findings = %q; want the test file and the list, naming the commit and the restore", got)
	}

	// A rename is not a deletion.
	git("mv", "b/keep_test.go", "b/keep_functional_test.go")
	git("commit", "-q", "-m", "split")
	if got := findings(); len(got) != 0 {
		t.Fatalf("a rename: findings = %q; want none", got)
	}

	// A row added in the same change declares the deletion; a row that
	// names no deletion of this change is red, as is a row with no why.
	remove("b/keep_functional_test.go")
	write(deletedTestsLogPath, "# the log\nb/keep_functional_test.go its cases moved to the functional tier\nd/none_test.go never existed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "declared")
	got = findings()
	if len(got) != 1 || !strings.Contains(got[0], `"d/none_test.go"`) || !strings.Contains(got[0], "deletes no such file") {
		t.Fatalf("declared deletion: findings = %q; want only the row that names no deletion", got)
	}

	// An old row declares nothing for a later change: the same file, put
	// back and deleted again with no new row, is red.
	write("b/keep_functional_test.go", "package b\n")
	git("add", "-A")
	git("commit", "-q", "-m", "put back")
	remove("b/keep_functional_test.go")
	git("add", "-A")
	git("commit", "-q", "-m", "gone again")
	if got := findings(); len(got) != 1 || !strings.Contains(got[0], "deletes b/keep_functional_test.go") {
		t.Fatalf("an old row: findings = %q; want the deletion red", got)
	}
}

func TestGuardedByMergeRuleReadsThePath(t *testing.T) {
	t.Parallel()
	for rel, want := range map[string]bool{
		"internal/ci/silent_class_test.go":            true,
		"cmd/nova-sprint/silent_test.go":              true,
		"internal/nsprint/table/silent_test.go":       true,
		"internal/ci/testdata/silent_allowlist.txt":   true,
		"internal/ci/testdata/deleted-tests.txt":      true,
		"internal/ci/testdata/net/x.txt":              false,
		"internal/ci/testdata/lisptemppath/x_test.go": true,
		"cmd/nova-sprint/silent.go":                   false,
		"docs/SPEC-CI.md":                             false,
	} {
		if got := guardedByMergeRule(rel); got != want {
			t.Errorf("guardedByMergeRule(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestDeclaredRowsAddedReadsOnlyTheAddedRows(t *testing.T) {
	t.Parallel()
	diff := "--- a/internal/ci/testdata/deleted-tests.txt\n+++ b/internal/ci/testdata/deleted-tests.txt\n@@ -1,2 +1,4 @@\n # the log\n old/one_test.go kept from before\n+# a comment\n+new/two_test.go moved to the functional tier\n+new/three_test.go\n-gone/row_test.go a removed row\n"
	got := declaredRowsAdded(diff)
	if len(got) != 2 || got["new/two_test.go"] != "moved to the functional tier" || got["new/three_test.go"] != "" {
		t.Fatalf("declaredRowsAdded = %v; want the two added rows, the context, the comment and the removed row unread", got)
	}
}

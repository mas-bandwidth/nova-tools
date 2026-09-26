//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"testing"
)

// unitwaits_git_functional_test.go is the control of the SLEEPS ledger's
// merge-base rule (unitwaits_class_test.go) over a git repository it builds:
// ten git commands and seven commits, about a second, and a real child process
// each time, so it is the functional tier (#4328); the rule itself runs on the
// real tree in the unit tier.

// PROBES 2 and 3 of the #4413 ruling, at the merge base, over a repository
// this test builds: a ledger row the first parent has is green; a change that
// adds a SLEEPS skip and its row in the same diff is red, naming the row; a
// change that deletes a row is green (the ledger shrinks); a first parent with
// no ledger is the seed; on a branch past origin/dev the base is the merge
// base, so a row added two commits back is still red.
func TestSleepsLedgerGrowthIsReadOutOfGit(t *testing.T) {
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
	growth := func() ([]string, bool) {
		t.Helper()
		added, _, seed, err := sleepsLedgerGrowth(root)
		if err != nil {
			t.Fatal(err)
		}
		return added, seed
	}

	git("init", "-q")
	write("README", "base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	write(sleepsLedger, "cmd/a\tTestOld\tseed\n")
	write("cmd/a/a_test.go", "package a\n")
	git("add", "-A")
	git("commit", "-q", "-m", "seed")
	if added, seed := growth(); !seed || len(added) != 0 {
		t.Fatalf("a parent with no ledger: added %v seed %v; want the seed", added, seed)
	}

	// PROBE 2: a ledgered wait whose row the parent has is green.
	write("cmd/a/a_test.go", "package a\n\n// touched\n")
	git("add", "-A")
	git("commit", "-q", "-m", "touch")
	if added, seed := growth(); seed || len(added) != 0 {
		t.Fatalf("the row at the parent: added %v seed %v; want nothing added", added, seed)
	}

	// PROBE 3: the skip and its row in the same diff is red.
	write("cmd/a/new_test.go", "package a\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) { t.Skip(\"SLEEPS: waits\") }\n")
	write(sleepsLedger, "cmd/a\tTestOld\tseed\ncmd/a\tTestNew\tadded with its skip\n")
	git("add", "-A")
	git("commit", "-q", "-m", "a skip and its row")
	if added, _ := growth(); len(added) != 1 || added[0] != "cmd/a\tTestNew" {
		t.Fatalf("a skip and its row together: added %q; want [cmd/a\\tTestNew]", added)
	}

	// A deleted row is the ledger shrinking.
	write(sleepsLedger, "cmd/a\tTestNew\tadded with its skip\n")
	git("add", "-A")
	git("commit", "-q", "-m", "TestOld fixed")
	if added, _ := growth(); len(added) != 0 {
		t.Fatalf("a deleted row: added %q; want none", added)
	}

	// On a branch (origin/dev in the checkout, HEAD past it) the base is the
	// merge base, not the first parent: a row added two commits back is still
	// the branch's addition.
	git("update-ref", "refs/remotes/origin/dev", "HEAD")
	write("cmd/a/later_test.go", "package a\n\nimport \"testing\"\n\nfunc TestLater(t *testing.T) { t.Skip(\"SLEEPS: waits\") }\n")
	write(sleepsLedger, "cmd/a\tTestNew\tadded with its skip\ncmd/a\tTestLater\tadded on the branch\n")
	git("add", "-A")
	git("commit", "-q", "-m", "branch: a skip and its row")
	write("cmd/a/a_test.go", "package a\n\n// touched again\n")
	git("add", "-A")
	git("commit", "-q", "-m", "branch: an unrelated commit on top")
	if added, _ := growth(); len(added) != 1 || added[0] != "cmd/a\tTestLater" {
		t.Fatalf("a row added two commits back on a branch: added %q; want [cmd/a\\tTestLater] against the merge base", added)
	}
}

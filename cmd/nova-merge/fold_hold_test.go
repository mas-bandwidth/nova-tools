package main

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The tests for Stella's review of #1173 at 60a1a17e: six fold defects, one test each.

// Keep-both is a hunk-level union, not two whole files joined: a Go test file both sides
// appended to keeps both tests, its package clause and imports appear once, and it parses.
func TestFoldKeepBothIsAHunkUnionThatStillParses(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.write("feature_test.go", "package main\n\nimport \"testing\"\n\nfunc TestBase(t *testing.T) {}\n")
	l.commit("base test file")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/main")
	l.git(l.work, "fetch", "-q", "origin", "main")
	l.branch("test-a", "feature_test.go", "package main\n\nimport \"testing\"\n\nfunc TestBase(t *testing.T) {}\n\nfunc TestA(t *testing.T) {}\n", "testA")
	l.branch("test-b", "feature_test.go", "package main\n\nimport \"testing\"\n\nfunc TestBase(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n", "testB")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	greenTests(l)
	file := l.foldBranches("branches.txt", "test-a 1\ntest-b 2\n")

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 0 {
		t.Fatalf("fold: exit %d\n%s", exit, stderr)
	}
	contains(t, stdout, "FOLD OK folded=2 dropped=0")
	l.git(l.work, "fetch", "-q", "origin", "fold-out")
	got := l.git(l.work, "show", "FETCH_HEAD:feature_test.go")
	for _, one := range []string{"package main", `import "testing"`, "func TestBase("} {
		if n := strings.Count(got, one); n != 1 {
			t.Fatalf("%q appears %d times after keep-both, want once:\n%s", one, n, got)
		}
	}
	contains(t, got, "func TestA(")
	contains(t, got, "func TestB(")
	if _, err := parser.ParseFile(token.NewFileSet(), "feature_test.go", got, 0); err != nil {
		t.Fatalf("the keep-both result does not parse as Go: %v\n%s", err, got)
	}
	if left, _ := filepath.Glob(filepath.Join(l.lane, foldWorkDir, ".merge_file_*")); len(left) != 0 {
		t.Fatalf("keep-both left its stage files in the scratch clone: %v", left)
	}
}

// The keep-both write never follows a symlink: a conflict path that is a link, or whose
// parent directory is a link, to somewhere outside the scratch clone is refused and the
// outside file is untouched.
func TestFoldWriteUnderRefusesSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "target_test.go")
	if err := os.WriteFile(target, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "x_test.go")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"x_test.go", "sub/target_test.go"} {
		if err := foldWriteUnder(root, rel, []byte("written\n")); err == nil {
			t.Fatalf("foldWriteUnder wrote through the symlink at %s", rel)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != "outside\n" {
		t.Fatalf("the file outside the scratch clone was changed: %q", b)
	}
	plain := filepath.Join(root, "plain_test.go")
	if err := os.WriteFile(plain, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := foldWriteUnder(root, "plain_test.go", []byte("new\n")); err != nil {
		t.Fatalf("a plain file under the root was refused: %v", err)
	}
	info, _ := os.Stat(plain)
	if b, _ := os.ReadFile(plain); string(b) != "new\n" || info.Mode().Perm() != 0o755 {
		t.Fatalf("the plain file was not rewritten in place with its mode: %q %v", b, info.Mode())
	}
}

// --close-folded closes nothing while the fold's pull request is not merged.
func TestFoldCloseFoldedRefusesAnUnmergedFold(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.host.PRs[500] = merge.PR{Number: 500, Body: "supersedes #11 branch=feature-a\n"}
	l.host.PRs[11] = merge.PR{Number: 11, HeadRef: "feature-a"}

	exit, _, stderr := l.run("fold", "--lane", l.lane, "--close-folded", "--pr", "500")
	if exit != 2 {
		t.Fatalf("an unmerged fold: exit %d, want 2\n%s", exit, stderr)
	}
	contains(t, stderr, "not merged")
	if len(l.host.Closed) != 0 {
		t.Fatalf("an unmerged fold closed %v", l.host.Closed)
	}
}

// --close-folded closes only an open pull request whose head is the folded branch.
func TestFoldCloseFoldedChecksEachHeadIsTheFoldedBranch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.host.PRs[500] = merge.PR{Number: 500, Merged: true,
		Body: "supersedes #11 branch=feature-a\nsupersedes #12 branch=feature-b\nsupersedes #13 branch=feature-c\n"}
	l.host.PRs[11] = merge.PR{Number: 11, HeadRef: "feature-a"}
	l.host.PRs[12] = merge.PR{Number: 12, HeadRef: "someone-elses-branch"}
	l.host.PRs[13] = merge.PR{Number: 13, HeadRef: "feature-c", Closed: true}

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--close-folded", "--pr", "500")
	if exit != 0 {
		t.Fatalf("fold --close-folded: exit %d\n%s", exit, stderr)
	}
	contains(t, stdout, "FOLD CLOSED pr=500 closed=1")
	contains(t, stderr, "pr=12 reason=head-is-not-the-folded-branch")
	if !reflect.DeepEqual(l.host.Closed, []int{11}) {
		t.Fatalf("closed %v, want [11]", l.host.Closed)
	}
}

// A bare numeric card is a card, never a pull request number; only `#<n>` is.
func TestFoldBodySupersedesOnlyPullRequestReferences(t *testing.T) {
	t.Parallel()
	body := foldBody([]foldBranch{{Name: "feature-a", Cards: []string{"101", "#77", "card-9"}}}, "main", "fold-out")
	absent(t, body, "supersedes #101")
	contains(t, body, "supersedes #77 branch=feature-a")
	got := supersededPRs(body)
	if len(got) != 1 || got[0].PR != 77 || got[0].Branch != "feature-a" {
		t.Fatalf("supersededPRs read %v, want [{77 feature-a}]", got)
	}
}

// A mixed tree runs the nova-work script AND go test over the changed Go packages.
func TestFoldTreeCommandsRunGoForChangedPackagesInAMixedTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "lisp", "nova-work", "run-tests.sh"), "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(dir, "pkg", "a.go"), "package pkg\n")
	mustWriteFile(t, filepath.Join(dir, "a.go"), "package root\n")

	changed := []string{"pkg/a.go", "README.md", "gone/b.go", "pkg/a.go", "a.go"}
	// No go.mod: not a Go module, the script alone.
	if got := foldTreeCommands(dir, changed); !reflect.DeepEqual(got, [][]string{{"bash", "lisp/nova-work/run-tests.sh"}}) {
		t.Fatalf("a nova-work tree with no go.mod: %v", got)
	}
	mustWriteFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	want := [][]string{{"bash", "lisp/nova-work/run-tests.sh"}, {"go", "test", "./pkg", "."}}
	if got := foldTreeCommands(dir, changed); !reflect.DeepEqual(got, want) {
		t.Fatalf("a mixed tree: got %v, want %v", got, want)
	}
	if got := foldTreeCommands(dir, []string{"lisp/x.lisp"}); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("a mixed tree with no Go change: %v", got)
	}
	goOnly := t.TempDir()
	if got := foldTreeCommands(goOnly, nil); !reflect.DeepEqual(got, [][]string{{"go", "test", "./..."}}) {
		t.Fatalf("a Go tree: %v", got)
	}
}

// The changed files are the merge's own: HEAD against its first parent.
func TestFoldChangedFilesReadsTheMergesOwnChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=f", "-c", "user.email=f@localhost"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	mustWriteFile(t, filepath.Join(dir, "base.txt"), "base\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "side")
	mustWriteFile(t, filepath.Join(dir, "pkg", "a.go"), "package pkg\n")
	run("add", "-A")
	run("commit", "-q", "-m", "side")
	run("checkout", "-q", "main")
	mustWriteFile(t, filepath.Join(dir, "main-only.txt"), "m\n")
	run("add", "-A")
	run("commit", "-q", "-m", "main")
	run("merge", "-q", "--no-ff", "-m", "fold: side", "side")

	got, err := foldChangedFiles(dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"pkg/a.go"}) {
		t.Fatalf("changed files %v, want [pkg/a.go]", got)
	}
}

// A test command is bounded: past its timeout its whole process group is killed and the
// fold hears red, rather than waiting on it forever.
func TestRunFoldCmdIsBoundedByItsTimeout(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("sh and sleep are the fixture")
	}
	start := time.Now()
	err := runFoldCmd(300*time.Millisecond, t.TempDir(), "sh", "-c", "sleep 30 & sleep 30")
	if err == nil {
		t.Fatal("a command that outlived its timeout was reported green")
	}
	if !strings.Contains(err.Error(), "took longer than") {
		t.Fatalf("the timeout was not named: %v", err)
	}
	if wall := time.Since(start); wall > 15*time.Second {
		t.Fatalf("runFoldCmd returned after %s; the timeout did not bound it", wall)
	}
}

// lostReplyRunner runs the lease push and then reports an error, the way a push whose
// reply was lost on the wire looks to the caller; land=false drops the push entirely.
type lostReplyRunner struct {
	inner merge.Runner
	land  bool
	// advance, when set, names a bare repository whose ref is moved on by another
	// writer right after the push lands and before the fold reads it back.
	advance string
	ref     string
}

func (r *lostReplyRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if isLeasePush(args) {
		if r.land {
			_, _ = r.inner.Run(ctx, dir, name, args...)
			if r.advance != "" {
				landed, err := r.inner.Run(ctx, r.advance, "git", "rev-parse", r.ref)
				if err != nil {
					return "", err
				}
				landed = strings.TrimSpace(landed)
				other, err := r.inner.Run(ctx, r.advance, "git", "-c", "user.name=other", "-c", "user.email=other@example.invalid",
					"commit-tree", landed+"^{tree}", "-p", landed, "-m", "another writer")
				if err != nil {
					return "", err
				}
				if _, err := r.inner.Run(ctx, r.advance, "git", "update-ref", r.ref, strings.TrimSpace(other), landed); err != nil {
					return "", err
				}
			}
		}
		return "fatal: the remote end hung up unexpectedly", errors.New("exit status 128")
	}
	return r.inner.Run(ctx, dir, name, args...)
}

// A lease push whose reply is lost is read back from the remote before anything is said:
// if the remote holds the squash the fold goes on and opens its pull request; only when it
// does not is "nothing was published" printed.
func TestFoldReadsTheRemoteBackWhenThePushReplyIsLost(t *testing.T) {
	t.Parallel()
	for _, land := range []bool{true, false} {
		l := newLab(t)
		l.init("main")
		l.branch("feature-a", "a.txt", "a\n", "a")
		l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
		before := l.git(l.remote, "rev-parse", "refs/heads/fold-out")
		greenTests(l)
		l.runner = &lostReplyRunner{inner: merge.Exec{}, land: land}
		file := l.foldBranches("branches.txt", "feature-a 101\n")

		exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
		after := l.git(l.remote, "rev-parse", "refs/heads/fold-out")
		if land {
			if exit != 0 || !strings.Contains(stdout, "FOLD OK folded=1 dropped=0 pr=") {
				t.Fatalf("a landed push with a lost reply: exit %d\n%s\n%s", exit, stdout, stderr)
			}
			absent(t, stderr, "nothing was published")
			contains(t, stderr, "reply was lost")
			if after == before {
				t.Fatal("the fixture did not land the push")
			}
			continue
		}
		if exit != 1 {
			t.Fatalf("a push that did not land: exit %d, want 1\n%s", exit, stderr)
		}
		contains(t, stderr, "nothing was published")
		if after != before {
			t.Fatal("the fixture landed a push it was told to drop")
		}
	}
}

// A push whose reply is lost and whose ref another writer then moves on proves nothing
// either way: the squash may have landed and been superseded. Only a ref still at the
// pre-push expected sha proves the lease push did not land, so a ref at any other sha is
// reported unknown, never "nothing was published", and no pull request is opened on it.
func TestFoldALostReplyOnARefThatMovedOnIsUnknown(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.branch("feature-a", "a.txt", "a\n", "a")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	before := l.git(l.remote, "rev-parse", "refs/heads/fold-out")
	greenTests(l)
	l.runner = &lostReplyRunner{inner: merge.Exec{}, land: true, advance: l.remote, ref: "refs/heads/fold-out"}
	file := l.foldBranches("branches.txt", "feature-a 101\n")

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	after := l.git(l.remote, "rev-parse", "refs/heads/fold-out")
	if after == before {
		t.Fatal("the fixture did not land and advance the push")
	}
	if exit != 1 {
		t.Fatalf("a lost reply on a ref that moved on: exit %d, want 1\n%s\n%s", exit, stdout, stderr)
	}
	absent(t, stderr, "nothing was published")
	absent(t, stdout, "FOLD OK")
	contains(t, stderr, "unknown")
	contains(t, stderr, merge.Short(after))
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

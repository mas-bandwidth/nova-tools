package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The red tests for docs/SPEC-MERGE.md "The fold (#1142)". Each is written first and
// seen red, with a fake where the real thing is the network, a bench or a clock.

// argLog records every git argument list this tool hands the runner, so a test can prove
// that the one push of a fold is a lease and that no plain push and no --force were built.
type argLog struct {
	inner merge.Runner
	mu    sync.Mutex
	args  [][]string
}

func (a *argLog) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	a.mu.Lock()
	a.args = append(a.args, append([]string{dir}, args...))
	a.mu.Unlock()
	return a.inner.Run(ctx, dir, name, args...)
}

func (a *argLog) pushesTo(ref string) [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out [][]string
	for _, call := range a.args {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, " push ") && strings.Contains(joined, ref) {
			out = append(out, call)
		}
	}
	return out
}

func (a *argLog) all() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]string(nil), a.args...)
}

// foldBranches writes the --branches file: one branch per line, each carrying its cards.
func (l *lab) foldBranches(name, body string) string {
	l.t.Helper()
	path := filepath.Join(l.dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	return path
}

func foldPR(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "FOLD OK ") {
			for _, tok := range strings.Fields(line) {
				if strings.HasPrefix(tok, "pr=") {
					return strings.TrimPrefix(tok, "pr=")
				}
			}
		}
	}
	t.Fatalf("no FOLD OK line in:\n%s", stdout)
	return ""
}

func greenTests(l *lab) {
	l.testTree = func(string) error { return nil }
	l.testLayout = func(string) error { return nil }
}

// Demanded test 1. A fold of three branches against a green fake test runner squashes to
// one commit whose message lists all three branches and their cards, the fake remote sees
// one lease push, and the line reads FOLD OK folded=3 dropped=0 pr=<n>.
func TestFoldOfThreeBranchesSquashesToOneLeasePush(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.branch("feature-a", "a.txt", "a\n", "a")
	l.branch("feature-b", "b.txt", "b\n", "b")
	l.branch("feature-c", "c.txt", "c\n", "c")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	greenTests(l)
	file := l.foldBranches("branches.txt", "feature-a 101 102\nfeature-b 103\nfeature-c 104\n")
	before := len(l.pushes())

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 0 {
		t.Fatalf("fold: exit %d\n%s", exit, stderr)
	}
	if !strings.Contains(stdout, "FOLD OK folded=3 dropped=0 pr=") {
		t.Fatalf("want FOLD OK folded=3 dropped=0, got:\n%s", stdout)
	}
	if pr := foldPR(t, stdout); pr == "" || pr == "0" {
		t.Fatalf("FOLD OK named no pull request:\n%s", stdout)
	}

	l.git(l.work, "fetch", "-q", "origin", "fold-out")
	msg := l.git(l.work, "log", "-1", "--format=%B", "FETCH_HEAD")
	for _, want := range []string{"feature-a", "feature-b", "feature-c", "101", "102", "103", "104"} {
		contains(t, msg, want)
	}
	files := l.git(l.work, "ls-tree", "-r", "--name-only", "FETCH_HEAD")
	for _, want := range []string{"a.txt", "b.txt", "c.txt"} {
		contains(t, files, want)
	}
	pushes := l.pushes()
	if len(pushes)-before != 1 {
		t.Fatalf("the remote saw %d pushes during the fold, want exactly 1:\n%v", len(pushes)-before, pushes)
	}
	if last := pushes[len(pushes)-1]; !strings.Contains(last, "refs/heads/fold-out") {
		t.Fatalf("the one push during the fold was not to fold-out: %s", last)
	}
}

// Demanded test 2. A fake runner red for one branch makes the fold try it three times and
// no more, drop it, print dropped=1, and leave its tree out of the squash.
func TestFoldDropsABranchRedAfterThreeTries(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.branch("feature-a", "a.txt", "a\n", "a")
	l.branch("feature-b", "b.txt", "b\n", "b")
	l.branch("feature-c", "c.txt", "c\n", "c")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	tries := 0
	l.testTree = func(dir string) error {
		if _, err := os.Stat(filepath.Join(dir, "b.txt")); err == nil {
			tries++
			return fmt.Errorf("b.txt is red")
		}
		return nil
	}
	l.testLayout = func(string) error { return nil }
	file := l.foldBranches("branches.txt", "feature-a 101\nfeature-b 102\nfeature-c 103\n")

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 0 {
		t.Fatalf("fold: exit %d\n%s", exit, stderr)
	}
	if !strings.Contains(stdout, "FOLD OK folded=2 dropped=1 pr=") {
		t.Fatalf("want FOLD OK folded=2 dropped=1, got:\n%s", stdout)
	}
	if tries != 3 {
		t.Fatalf("a red branch was tried %d times, want exactly three", tries)
	}
	l.git(l.work, "fetch", "-q", "origin", "fold-out")
	files := l.git(l.work, "ls-tree", "-r", "--name-only", "FETCH_HEAD")
	contains(t, files, "a.txt")
	contains(t, files, "c.txt")
	absent(t, files, "b.txt")
}

// Demanded test 3. A fake git merge conflicting in a test file and in a source file
// yields keep-both and the incoming side in the scratch tree; a conflict in any other
// file drops the branch with that file named.
func TestFoldResolvesTestAndSourceConflictsAndDropsOther(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.write("feature_test.go", "package main\n// base\n")
	l.write("main.go", "package main\n// base\n")
	l.write("data.txt", "base\n")
	l.commit("base files")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/main")
	l.git(l.work, "fetch", "-q", "origin", "main")

	l.branch("test-a", "feature_test.go", "package main\n// testA\n", "testA")
	l.branch("test-b", "feature_test.go", "package main\n// testB\n", "testB")
	l.branch("src-a", "main.go", "package main\n// srcA\n", "srcA")
	l.branch("src-b", "main.go", "package main\n// srcB\n", "srcB")
	l.branch("other-a", "data.txt", "otherA\n", "otherA")
	l.branch("other-b", "data.txt", "otherB\n", "otherB")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	greenTests(l)
	file := l.foldBranches("branches.txt",
		"test-a 1\ntest-b 2\nsrc-a 3\nsrc-b 4\nother-a 5\nother-b 6\n")

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 0 {
		t.Fatalf("fold: exit %d\n%s", exit, stderr)
	}
	if !strings.Contains(stdout, "FOLD OK folded=5 dropped=1") {
		t.Fatalf("want five folded and one dropped, got:\n%s", stdout)
	}
	contains(t, stderr, "data.txt")
	l.git(l.work, "fetch", "-q", "origin", "fold-out")
	testFile := l.git(l.work, "show", "FETCH_HEAD:feature_test.go")
	contains(t, testFile, "testA")
	contains(t, testFile, "testB")
	srcFile := l.git(l.work, "show", "FETCH_HEAD:main.go")
	contains(t, srcFile, "srcB")
	absent(t, srcFile, "srcA")
	data := l.git(l.work, "show", "FETCH_HEAD:data.txt")
	contains(t, data, "otherA")
	absent(t, data, "otherB")
}

// Demanded test 4. A fake remote that rejects the lease prints one refusal, says nothing
// was published, and records no plain push and no --force.
func TestFoldARejectedLeasePublishesNothing(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	oid := l.branch("feature-a", "a.txt", "a\n", "a")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	greenTests(l)
	log := &argLog{inner: merge.Exec{}}
	l.runner = &hookRunner{inner: log, before: func(_ string, args []string) {
		if len(args) > 0 && args[0] == "push" && isLeasePush(args) {
			l.git(l.remote, "update-ref", "refs/heads/fold-out", oid)
		}
	}}
	file := l.foldBranches("branches.txt", "feature-a 101\n")

	exit, _, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 1 {
		t.Fatalf("a rejected lease: exit %d, want 1\n%s", exit, stderr)
	}
	contains(t, stderr, "nothing was published")
	for _, call := range log.all() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "--force ") || strings.HasSuffix(joined, " --force") || strings.Contains(joined, " -f ") {
			t.Fatalf("a force spelling reached the runner: %s", joined)
		}
		if strings.Contains(joined, "push") && strings.Contains(joined, "refs/heads/fold-out") && !strings.Contains(joined, "--force-with-lease=") {
			t.Fatalf("a plain push to fold-out reached the runner: %s", joined)
		}
	}
}

// Demanded test 5. A fake clock proves the three tries are bounded and the fold ends on
// its own.
func TestFoldThreeTriesAreBoundedByTheClock(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.branch("feature-a", "a.txt", "a\n", "a")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	tries := 0
	l.testTree = func(string) error { tries++; return fmt.Errorf("always red") }
	l.testLayout = func(string) error { return nil }
	start := l.now
	file := l.foldBranches("branches.txt", "feature-a 101\n")

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 0 {
		t.Fatalf("fold: exit %d\n%s", exit, stderr)
	}
	if tries != 3 {
		t.Fatalf("the fold tried a red branch %d times, want exactly three", tries)
	}
	if !strings.Contains(stdout, "FOLD OK folded=0 dropped=1") {
		t.Fatalf("want folded=0 dropped=1, got:\n%s", stdout)
	}
	if !l.now.After(start) {
		t.Fatalf("the three tries spent no clock at all: start=%s now=%s", start, l.now)
	}
}

// Demanded test 6. --close-folded --pr <n> against a fake host records each folded pull
// request closed superseded by the squash, and prints FOLD CLOSED pr=<n> closed=<n>.
func TestFoldCloseFoldedClosesSupersededPullRequests(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.host.PRs[500] = merge.PR{Number: 500, Body: "nova-merge fold of fold-out onto main\n\nsupersedes #11\nsupersedes #12\n"}

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--close-folded", "--pr", "500")
	if exit != 0 {
		t.Fatalf("fold --close-folded: exit %d\n%s", exit, stderr)
	}
	if !strings.Contains(stdout, "FOLD CLOSED pr=500 closed=2") {
		t.Fatalf("want FOLD CLOSED pr=500 closed=2, got:\n%s", stdout)
	}
	if len(l.host.Closed) != 2 || l.host.Closed[0] != 11 || l.host.Closed[1] != 12 {
		t.Fatalf("closed %v, want [11 12]", l.host.Closed)
	}
}

// Demanded test 7. No --branches, no --onto, no --out and --close-folded without --pr
// are each exit 2 with their one remedy line; a directory that is not a lane is exit 2
// with the init remedy.
func TestFoldRefusalsNameTheirRemedy(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	file := l.foldBranches("branches.txt", "feature-a 101\n")

	cases := []struct {
		name   string
		args   []string
		remedy string
	}{
		{"no branches", []string{"fold", "--lane", l.lane, "--onto", "main", "--out", "fold-out"}, "nova-merge fold --branches <file> --onto <base> --out <branch>"},
		{"no onto", []string{"fold", "--lane", l.lane, "--branches", file, "--out", "fold-out"}, "--onto <base>"},
		{"no out", []string{"fold", "--lane", l.lane, "--branches", file, "--onto", "main"}, "--out <branch>"},
		{"close without pr", []string{"fold", "--lane", l.lane, "--close-folded"}, "nova-merge fold --close-folded --pr <n>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := l.run(tc.args...)
			if exit != 2 {
				t.Fatalf("exit %d, want 2\n%s", exit, stderr)
			}
			contains(t, stderr, tc.remedy)
		})
	}

	notALane := filepath.Join(l.dir, "not-a-lane")
	if err := os.MkdirAll(notALane, 0o755); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := l.run("fold", "--lane", notALane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 2 {
		t.Fatalf("a directory that is not a lane: exit %d, want 2\n%s", exit, stderr)
	}
	contains(t, stderr, "refusing to guess")
	contains(t, stderr, "nova-merge init")
}

// Demanded test 8. The layout test of #560 runs after every merge; a fake run that fails
// it drops that branch like any other red.
func TestFoldRunsTheLayoutTestAfterEveryMerge(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.branch("feature-a", "a.txt", "a\n", "a")
	l.git(l.work, "push", "-q", "origin", "main:refs/heads/fold-out")
	layoutCalls := 0
	l.testTree = func(string) error { return nil }
	l.testLayout = func(dir string) error {
		layoutCalls++
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
			return fmt.Errorf("the layout test of #560 is red")
		}
		return nil
	}
	file := l.foldBranches("branches.txt", "feature-a 101\n")

	exit, stdout, stderr := l.run("fold", "--lane", l.lane, "--branches", file, "--onto", "main", "--out", "fold-out")
	if exit != 0 {
		t.Fatalf("fold: exit %d\n%s", exit, stderr)
	}
	if layoutCalls != 3 {
		t.Fatalf("the layout test ran %d times, want three tries after the merge", layoutCalls)
	}
	if !strings.Contains(stdout, "FOLD OK folded=0 dropped=1") {
		t.Fatalf("a red layout test must drop the branch, got:\n%s", stdout)
	}
}

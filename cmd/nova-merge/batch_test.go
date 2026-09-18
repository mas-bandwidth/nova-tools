package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The landing gate's own tests. They drive cmdBatch through run(), against a REAL git
// and a bare fixture repository in t.TempDir(), and they reach no network: the clone
// URL comes from Deps.RepoURL, which the lab points at its own bare repository, and the
// module the gate builds has no dependencies.

// batchRepo builds the fixture: a bare remote holding a dev branch with a tiny go
// module, and three pull request heads under refs/pull/<n>/head.
//
//	#1 adds pkg/a and changes base/shared.go -- clean on top of dev
//	#2 changes the same line of base/shared.go -- CONFLICTS once #1 is ahead of it
//	#3 adds pkg/c with a test that fails -- green to build and vet, RED at go test
//
// That is the shape the shell script found on hulk: a member that will not merge, and a
// member that merges and then poisons the batch.
func batchRepo(t *testing.T) *lab {
	t.Helper()
	l := newLab(t)
	l.git(l.work, "checkout", "-q", "-B", "dev", "origin/main")
	l.write("go.mod", "module example.com/batch\n\ngo 1.21\n")
	l.write("base/base.go", "package base\n\nfunc Base() int { return 1 }\n")
	l.write("base/shared.go", "package base\n\nvar Shared = \"base\"\n")
	dev := l.commit("dev base")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	l.git(l.work, "checkout", "-q", "-B", "pr1", dev)
	l.write("pkg/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	l.write("base/shared.go", "package base\n\nvar Shared = \"pr1\"\n")
	one := l.commit("pr 1")

	l.git(l.work, "checkout", "-q", "-B", "pr2", dev)
	l.write("base/shared.go", "package base\n\nvar Shared = \"pr2\"\n")
	two := l.commit("pr 2")

	l.git(l.work, "checkout", "-q", "-B", "pr3", dev)
	l.write("pkg/c/c.go", "package c\n\nfunc C() int { return 3 }\n")
	l.write("pkg/c/c_test.go", "package c\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"the poison\") }\n")
	three := l.commit("pr 3")

	for n, sha := range map[int]string{1: one, 2: two, 3: three} {
		l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
	}
	l.git(l.work, "checkout", "-q", "main")
	return l
}

// remoteRefs is every ref the bare fixture repository holds, so a test can say that the
// batch's branch never reached it.
func remoteRefs(l *lab) string {
	return l.git(l.remote, "for-each-ref", "--format=%(refname)")
}

// THE RED RUN. #2 conflicts and is dropped by name, #1 and #3 merge, and #3's failing
// test turns the batch red: one line naming the step, the failing package and the
// failing test, exit 1, and nothing pushed.
func TestBatchDropsTheConflictAndGoesRedOnTheFailingMember(t *testing.T) {
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	before := len(l.pushes())

	exit, stdout, stderr := l.run("batch", "--name", "integration-1", "--pr", "1,2,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 1 {
		t.Fatalf("a red batch is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH FAIL name=integration-1")
	contains(t, stdout, "members=1,3")
	contains(t, stdout, "dropped=2")
	contains(t, stdout, "step=test")
	contains(t, stdout, "packages=example.com/batch/pkg/c")
	contains(t, stdout, "tests=TestBroken")
	absent(t, stdout, "BATCH OK")

	// THE PROGRESS REACHES THE STDERR WRITER, with elapsed time, while it runs: a gate
	// that says nothing for three minutes and a gate that has hung read the same.
	contains(t, stderr, "BATCH DROP #2 reason=\"the merge conflicts with the members ahead\" t=")
	contains(t, stderr, "BATCH MERGED #1 t=")
	contains(t, stderr, "BATCH MERGED #3 t=")
	contains(t, stderr, "BATCH STEP build ")
	// AND THE TEST STEP RAN THE WAY CI RUNS IT: the failing test is still named on the
	// FAIL line above, out of a -json stream rather than out of go test's text output.
	contains(t, stderr, "BATCH STEP test command=\"go test -json -count=1 -timeout 5m ./...\"")

	// NOTHING IS PUSHED. The caller pushes the branch and opens the pull request; the
	// verb has no path to a push at all, and the fixture's update hook recorded none.
	if got := len(l.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; the batch pushes nothing", got-before)
	}
	absent(t, remoteRefs(l), "rowan/integration-1")
}

// THE GREEN RUN, and the exact shape of the line a caller parses. #2 still conflicts and
// is still dropped, out loud, and the head named is the branch the caller then pushes.
func TestBatchOKNamesTheBaseTheHeadAndTheDroppedMember(t *testing.T) {
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	before := len(l.pushes())

	exit, stdout, stderr := l.run("batch", "--name", "integration-2", "--pr", "1 2",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("a green batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	clone := filepath.Join(root, "integration-2", "repo")
	base := l.git(l.work, "rev-parse", "dev")
	head := l.git(clone, "rev-parse", "refs/heads/rowan/integration-2")
	want := fmt.Sprintf("BATCH OK name=integration-2 base=%s head=%s members=1 dropped=2\n", base, head)
	if !strings.Contains(stdout, want) {
		t.Errorf("want the one-line shape\n\t%s\ngot:\n%s", want, stdout)
	}
	if head == base {
		t.Error("the head is the base; #1 was supposed to be merged onto it")
	}
	// The lisp leg is SKIPPED OUT LOUD here: this fixture holds no tools/ci/lisp-test.sh.
	// A gate that quietly ran three steps instead of four is a gate nobody can read.
	contains(t, stderr, "BATCH SKIP lisp ")
	if got := len(l.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; the batch pushes nothing", got-before)
	}
	absent(t, remoteRefs(l), "rowan/integration-2")
}

// The four flags nothing may guess, refused together in one go rather than one per run.
func TestBatchRefusesTheFlagsItWillNotGuess(t *testing.T) {
	l := batchRepo(t)
	exit, stdout, stderr := l.run("batch")
	if exit != 2 {
		t.Fatalf("a batch with no flags is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	for _, flag := range []string{"--name is required", "--pr is required", "--root is required", "--repo is required"} {
		contains(t, stderr, flag)
	}
	absent(t, stdout, "BATCH")
}

// --name is the directory under --root and half the branch name, so it is ONE path
// element and nothing that could climb out of the root the caller named.
func TestBatchRefusesANameThatIsNotOnePathElement(t *testing.T) {
	l := batchRepo(t)
	for _, bad := range []string{"../escape", "a/b", "-x"} {
		exit, stdout, stderr := l.run("batch", "--name", bad, "--pr", "1",
			"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"))
		if exit != 2 {
			t.Errorf("--name %q is exit 2, got %d\nstderr: %s", bad, exit, stderr)
		}
		contains(t, stderr, "--name is one path element")
		absent(t, stdout, "BATCH")
	}
}

// A --pr list holding something that is not a pull request number is refused before any
// directory is removed or any clone is made.
func TestBatchRefusesAPRListThatIsNotNumbers(t *testing.T) {
	l := batchRepo(t)
	exit, stdout, stderr := l.run("batch", "--name", "integration-3", "--pr", "1,two",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"))
	if exit != 2 {
		t.Fatalf("a bad --pr list is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "not a pull request number")
	absent(t, stdout, "BATCH")
}

// Rule 13, carried into the CHILDREN the gate starts: every check runs with its temp
// directory inside the batch's own working directory. An sbcl suite and a go build that
// key off the ambient one write into /tmp instead, and two gates on one host wrecked
// each other's state that way (tools/ci/lisp-test.sh, 2026-09-18).
func TestPrivateTempEnvPointsEveryTempVariableAtTheBatchsOwnDirectory(t *testing.T) {
	t.Parallel()
	got := privateTempEnv([]string{"PATH=/bin", "TMPDIR=/tmp/ambient", "HOME=/home/x", "GOTMPDIR=/tmp/ambient"}, "/w/tmp")
	seen := map[string]int{}
	for _, kv := range got {
		key, value, _ := strings.Cut(kv, "=")
		seen[key]++
		if strings.Contains(key, "TMP") || strings.Contains(key, "TEMP") {
			if value != "/w/tmp" {
				t.Errorf("%s is %q, want the batch's own temp directory", key, value)
			}
		}
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "GOTMPDIR", "LISP_TEST_TMPROOT"} {
		if seen[key] != 1 {
			t.Errorf("the child environment holds %s %d times, want exactly one", key, seen[key])
		}
	}
}

// THE GATE TESTS THE WAY CI TESTS, and this reads both sides so it goes red the day
// either moves. integration-4 ran green on hulk under a plain `go test ./...` and three
// CI legs then failed, because CI does not run a plain `go test ./...`: it runs the
// command in ci.yml's `test` step, and a gate that tests differently from CI is a gate
// that passes what CI fails.
func TestTheGateTestsTheWayCIDoes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	step := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "go test -json") {
			step = line
			break
		}
	}
	if step == "" {
		t.Fatal("no `go test -json` line in .github/workflows/ci.yml; the gate mirrors that step, so this test is reading the wrong file or the step has been rewritten -- update ciTestArgs in batch.go with whatever CI runs now")
	}
	got := strings.Join(ciTestArgs(), " ")
	for _, flag := range []string{"-json", "-count=1", "-timeout 5m"} {
		if !strings.Contains(step, flag) {
			t.Errorf("ci.yml's test step no longer carries %q; whatever it carries now is what ciTestArgs must mirror\nci.yml: %s", flag, strings.TrimSpace(step))
		}
		if !strings.Contains(got, flag) {
			t.Errorf("the gate's test command is %q and does not carry %q, which ci.yml's test step does", got, flag)
		}
	}
	if !strings.HasPrefix(got, "go test ") || !strings.HasSuffix(got, " ./...") {
		t.Errorf("the gate's test command is %q; it is `go test <the ci.yml flags> ./...`, the whole merged tree in one run where CI splits it across the matrix", got)
	}
}

// The -json stream is read with the SAME decoder cmd/nova-ci slowtests reads it with, so
// the gate and the budget check cannot disagree about what the stream said.
func TestTestFailuresReadsTheJSONStream(t *testing.T) {
	t.Parallel()
	stream := `{"Action":"run","Package":"example.com/batch/pkg/c","Test":"TestBroken"}
{"Action":"output","Package":"example.com/batch/pkg/c","Test":"TestBroken","Output":"    c_test.go:5: the poison\n"}
{"Action":"fail","Package":"example.com/batch/pkg/c","Test":"TestBroken","Elapsed":0}
{"Action":"pass","Package":"example.com/batch/pkg/a","Elapsed":0.01}
{"Action":"fail","Package":"example.com/batch/pkg/c","Elapsed":0.123}
`
	pkgs, tests, ok := testFailures(stream)
	if !ok {
		t.Fatal("a stream that is all JSON must read as JSON")
	}
	if len(pkgs) != 1 || pkgs[0] != "example.com/batch/pkg/c" {
		t.Errorf("packages = %v, want [example.com/batch/pkg/c]", pkgs)
	}
	if len(tests) != 1 || tests[0] != "TestBroken" {
		t.Errorf("tests = %v, want [TestBroken]", tests)
	}
	// A build failure writes plain text on stderr and runCheck captures both streams, so
	// a stream that is not all JSON is NOT read as an empty one -- it falls back to the
	// text reader, and a gate that read it as empty would print no failing package at all.
	if _, _, ok := testFailures("# example.com/batch/pkg/c\nc.go:3: undefined: X\nFAIL\texample.com/batch/pkg/c [build failed]\n"); ok {
		t.Error("a build failure's plain text read as a JSON stream; it must fall back to the text reader")
	}
}

// The failing packages and tests on the FAIL line are read out of the go test run's own
// output, so a caller sees what to look at without opening the log.
func TestFailuresInReadsThePackagesAndTests(t *testing.T) {
	t.Parallel()
	out := "--- FAIL: TestBroken (0.00s)\n    c_test.go:5: the poison\nFAIL\nFAIL\texample.com/batch/pkg/c\t0.123s\nok  \texample.com/batch/pkg/a\t0.010s\nFAIL\n"
	pkgs, tests := failuresIn(out)
	if len(pkgs) != 1 || pkgs[0] != "example.com/batch/pkg/c" {
		t.Errorf("packages = %v, want [example.com/batch/pkg/c]", pkgs)
	}
	if len(tests) != 1 || tests[0] != "TestBroken" {
		t.Errorf("tests = %v, want [TestBroken]", tests)
	}
}

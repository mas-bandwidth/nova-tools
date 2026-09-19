package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every fixture here is a real git repository with a real Go module, built in the test,
// because the thing under test is what `git` and `go test` do to a tree and no fake of
// either would prove it. A fixture is two commits: `base`, and a `head` that changes the
// source, the test, or both.

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a repository whose base commit holds a module, a package with a Sign
// function that is wrong at zero, and one test that does not notice.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q", "-b", "main")
	run(t, dir, "git", "config", "user.email", "fixture@example.com")
	run(t, dir, "git", "config", "user.name", "Fixture")
	write(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	write(t, dir, "sign/sign.go", `package sign

// Sign is wrong at zero: it answers -1.
func Sign(n int) int {
	if n > 0 {
		return 1
	}
	return -1
}
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}
`)
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-q", "-m", "base")
	return dir
}

func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-q", "-m", msg)
	return strings.TrimSpace(run(t, dir, "git", "rev-parse", "HEAD"))
}

// mutateFixture runs the verb over a fixture repo with the throwaway worktree under this
// test's OWN temp directory. Nothing here is asserted about the shared os.TempDir(): it is
// shared with every other job on the same self-hosted runner, and reading it is the flake
// the TempRoot option exists to end.
func mutateFixture(t *testing.T, dir string) (*MutateResult, error) {
	t.Helper()
	return mutateFixtureIn(t, dir, t.TempDir())
}

// mutateFixtureIn is mutateFixture with the temp root named, for the one test that has to
// look inside it afterwards.
func mutateFixtureIn(t *testing.T, dir, tempRoot string) (*MutateResult, error) {
	t.Helper()
	return mutateFixtureRefs(t, dir, "main", "HEAD", tempRoot)
}

// mutateFixtureRefs names the base and the head, for the tests whose fixture leaves the
// working copy on some other commit than the head.
func mutateFixtureRefs(t *testing.T, dir, base, head, tempRoot string) (*MutateResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return Mutate(ctx, MutateOptions{Repo: dir, Base: base, Head: head, TempRoot: tempRoot})
}

// The case the verb exists for: the head fixes Sign at zero and brings a test that is red
// without the fix. With the non-test hunk reverted the new test fails, the old one still
// passes, and the verdict is PASS -- the per-file rule is "at least one failing test",
// never "every test fails".
func TestMutatePassesWhenTheNewTestIsRedWithoutTheChange(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fix")
	write(t, dir, "sign/sign.go", `package sign

// Sign answers zero at zero.
func Sign(n int) int {
	if n > 0 {
		return 1
	}
	if n == 0 {
		return 0
	}
	return -1
}
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestSignZero(t *testing.T) {
	if Sign(0) != 0 {
		t.Fatalf("Sign(0) = %d, want 0", Sign(0))
	}
}
`)
	commit(t, dir, "fix Sign at zero, with its red test")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Fatalf("verdict FAIL, want PASS: red=%d green=%d greens=%v skips=%v", res.Red, res.Green, res.Greens, res.Skips)
	}
	if res.Red != 1 || res.Green != 1 {
		t.Fatalf("red=%d green=%d, want red=1 (TestSignZero) green=1 (TestSignPositive)", res.Red, res.Green)
	}
	if len(res.Greens) != 1 || res.Greens[0].Name != "TestSignPositive" {
		t.Fatalf("greens = %v, want TestSignPositive named", res.Greens)
	}
	if res.Verdict() != "PASS" {
		t.Fatalf("Verdict() = %q", res.Verdict())
	}
}

// The case a reader used to catch by hand and often did not: a test that passes with the
// change reverted proves nothing about the change. The verdict is FAIL and it NAMES the
// test, because "your test is green" is not actionable and "TestAddIsCommutative is green
// without your change" is.
func TestMutateFailsAndNamesATestThatIsGreenWithoutTheChange(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "cosmetic")
	write(t, dir, "sign/sign.go", `package sign

// Sign is rearranged and answers exactly what it answered before.
func Sign(n int) int {
	if n <= 0 {
		return -1
	}
	return 1
}
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestSignNegative(t *testing.T) {
	if Sign(-2) != -1 {
		t.Fatal("negative")
	}
}
`)
	commit(t, dir, "rearrange Sign, add a test that was always green")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Fatal("verdict PASS: a test green without the change proved nothing and was counted as proof")
	}
	if res.Red != 0 || res.Green != 2 {
		t.Fatalf("red=%d green=%d, want red=0 green=2", res.Red, res.Green)
	}
	var named []string
	for _, g := range res.Greens {
		named = append(named, g.Name)
	}
	if strings.Join(named, ",") != "TestSignNegative,TestSignPositive" {
		t.Fatalf("greens = %v; the FAIL must name the tests that stayed green", named)
	}
	if res.Greens[0].File != "sign/sign_test.go" {
		t.Fatalf("green file = %q, want the changed test file", res.Greens[0].File)
	}
}

// --head is a ref, and the tree it names is the throwaway worktree's, never the caller's
// checkout. A PR that ADDS a test file is the common shape, and a caller sitting on the
// base does not have that file: reading it from the caller's working copy skips the one
// file the range is about, and the verdict then rests on nothing.
func TestMutateReadsATestFileTheHeadAddsWhileTheCallerSitsOnTheBase(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fix")
	write(t, dir, "sign/sign.go", `package sign

// Sign answers zero at zero.
func Sign(n int) int {
	if n > 0 {
		return 1
	}
	if n == 0 {
		return 0
	}
	return -1
}
`)
	write(t, dir, "sign/zero_test.go", `package sign

import "testing"

func TestSignZero(t *testing.T) {
	if Sign(0) != 0 {
		t.Fatal("zero")
	}
}
`)
	commit(t, dir, "fix Sign at zero, in a test file of its own")
	run(t, dir, "git", "checkout", "-q", "main")

	res, err := mutateFixtureRefs(t, dir, "main", "fix", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skips) != 0 {
		t.Fatalf("skips = %+v; the head's test file is in the worktree the verb made at the head", res.Skips)
	}
	if !res.Pass || res.Red != 1 || res.Green != 0 {
		t.Fatalf("pass=%v red=%d green=%d greens=%v; the added test must be run and red", res.Pass, res.Red, res.Green, res.Greens)
	}
}

// The read rule, mechanised: a fix with no test is not admitted, and the refusal is the
// answer rather than a green run over somebody else's tests.
func TestMutateRefusesAChangeWithNoTestChange(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "untested")
	write(t, dir, "sign/sign.go", `package sign

// Sign answers zero at zero, and nothing here proves it.
func Sign(n int) int {
	if n > 0 {
		return 1
	}
	if n == 0 {
		return 0
	}
	return -1
}
`)
	head := commit(t, dir, "fix Sign at zero with no test")

	res, err := mutateFixture(t, dir)
	if !errors.Is(err, ErrNoTestsChanged) {
		t.Fatalf("err = %v, want ErrNoTestsChanged", err)
	}
	if res == nil || res.Head != head {
		t.Fatalf("the refusal must still carry the head it was asked about: %+v", res)
	}
}

// A test-only change has nothing to revert, so the run would be the head's own suite and
// its result -- green or red -- would say nothing about a change. The verb refuses rather
// than hand back a verdict it cannot mean.
func TestMutateRefusesWhenThereIsNoChangeToRevert(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "tests-only")
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestSignNegativeToo(t *testing.T) {
	if Sign(-1) != -1 {
		t.Fatal("negative")
	}
}
`)
	commit(t, dir, "more tests, no change")

	if _, err := mutateFixture(t, dir); !errors.Is(err, ErrNoChangeToRevert) {
		t.Fatalf("err = %v, want ErrNoChangeToRevert", err)
	}
}

// The worktree is the verb's one side effect on the caller's repo, and it must not
// outlive the call -- not on the passing path, and not on the path where the run fails.
// A `git worktree list` that grows by one per read is the repo's own record going wrong,
// and a checkout of somebody's head left in the temp directory is worse.
func TestMutateRemovesItsWorktreeOnBothPaths(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "cosmetic")
	write(t, dir, "sign/sign.go", `package sign

func Sign(n int) int {
	if n <= 0 {
		return -1
	}
	return 1
}
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestSignBig(t *testing.T) {
	if Sign(99) != 1 {
		t.Fatal("big")
	}
}
`)
	commit(t, dir, "a change whose test is green without it")

	before := strings.Count(run(t, dir, "git", "worktree", "list"), "\n")
	// The temp root is this test's own directory, and nothing else writes into it. The
	// assertion is therefore the flat one -- NOTHING is left behind -- with no snapshot
	// to take and no sibling to tell apart. Read against the shared os.TempDir() it was
	// red whenever another job on the same runner made a `nova-review-mutate-*` between
	// the snapshot and the check (#1341 twice, #1345, #1360); the class test
	// TestNoTestGlobsTheSharedTempDir in internal/ci holds the whole tree to this.
	tempRoot := t.TempDir()
	res, err := mutateFixtureIn(t, dir, tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Fatal("this fixture must FAIL; the removal is being proved on the failing path")
	}
	after := run(t, dir, "git", "worktree", "list")
	if strings.Count(after, "\n") != before {
		t.Fatalf("a worktree outlived the failing run:\n%s", after)
	}
	entries, err := filepath.Glob(filepath.Join(tempRoot, "nova-review-mutate-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a temp worktree directory was left behind under %s: %v", tempRoot, entries)
	}
}

// A package that cannot even compile with the change reverted is the strongest red there
// is, and the parse of `go test` output must read it that way: there are no per-test
// result lines to count, and counting none would report the tests as green.
func TestMutateCountsABuildFailureAsRed(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "newapi")
	write(t, dir, "sign/sign.go", `package sign

func Sign(n int) int {
	if n > 0 {
		return 1
	}
	return -1
}

// Abs is new in this change, and the new test cannot compile without it.
func Abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestAbs(t *testing.T) {
	if Abs(-3) != 3 {
		t.Fatal("abs")
	}
}
`)
	commit(t, dir, "add Abs with its test")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass || res.Green != 0 || res.Red != 2 {
		t.Fatalf("build failure not counted red: pass=%v red=%d green=%d greens=%v", res.Pass, res.Red, res.Green, res.Greens)
	}
}

// The revert is by file STATUS, not by patch: a file the head ADDED has no base version to
// check out, and `git checkout <base> -- <path>` on it fails. It is removed instead, and
// the test that needed it goes red for the right reason.
func TestMutateRemovesAFileTheHeadAdded(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "addfile")
	write(t, dir, "sign/abs.go", `package sign

func Abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestAbs(t *testing.T) {
	if Abs(-3) != 3 {
		t.Fatal("abs")
	}
}
`)
	commit(t, dir, "add abs.go with its test")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Fatalf("an added file was not reverted: red=%d green=%d greens=%v skips=%v", res.Red, res.Green, res.Greens, res.Skips)
	}
}

// A Lisp leg keeps its cases under tests/ and runs them with run-tests.sh. A project with
// no such script is SKIPPED WITH ITS REASON NAMED rather than silently counted either way:
// the one thing a mutation verdict must never be is quiet about what it did not run.
func TestMutateSkipsALispProjectWithNoRunner(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "lisp")
	write(t, dir, "lisp/nova-work/src/work.lisp", "(defun work () 1)\n")
	write(t, dir, "lisp/nova-work/tests/work-tests.lisp", "(assert (= (work) 1))\n")
	commit(t, dir, "a lisp change with no runner")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skips) != 1 || res.Skips[0].File != "lisp/nova-work/tests/work-tests.lisp" {
		t.Fatalf("skips = %+v, want the lisp test file named", res.Skips)
	}
	if !strings.Contains(res.Skips[0].Reason, "run-tests.sh") {
		t.Fatalf("skip reason = %q; it must name what was missing", res.Skips[0].Reason)
	}
	if res.Pass {
		t.Fatal("a run with nothing red is never PASS, however many files were skipped")
	}
}

// The Lisp path that does run: a suite that fails with the change reverted is red, exactly
// as a Go test function is, and the unit's name is the script the verb ran.
func TestMutateRunsALispSuiteAndCountsItRed(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "lisp")
	write(t, dir, "lisp/nova-work/run-tests.sh", "#!/bin/sh\nexec sh lisp/nova-work/tests/check.sh\n")
	write(t, dir, "lisp/nova-work/src/answer", "41\n")
	write(t, dir, "lisp/nova-work/tests/check.sh", "#!/bin/sh\ntest \"$(cat lisp/nova-work/src/answer)\" = 41\n")
	commit(t, dir, "base runner")
	run(t, dir, "git", "checkout", "-q", "main")
	run(t, dir, "git", "merge", "-q", "lisp")
	run(t, dir, "git", "checkout", "-q", "-b", "lispfix")
	write(t, dir, "lisp/nova-work/src/answer", "42\n")
	write(t, dir, "lisp/nova-work/tests/check.sh", "#!/bin/sh\ntest \"$(cat lisp/nova-work/src/answer)\" = 42\n")
	commit(t, dir, "the answer is 42, with its test")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass || res.Red != 1 {
		t.Fatalf("lisp suite not red without the change: pass=%v red=%d green=%d skips=%+v", res.Pass, res.Red, res.Green, res.Skips)
	}
}

// resolve is the whole of --test's rule, and the four answers it can give (#1849,
// Stella's ruling stella-e72bbf88a3f7). It is tested here rather than only through
// the verb because ONE of the four -- a unit whose file could not be run -- is a
// branch a fixture cannot reach on demand: a skip comes from a deleted test file or
// a runner that would not start, and neither leaves a unit behind to name. A rule
// that is only ever exercised by the paths that happen to be easy is a rule with a
// hole in exactly the place the ruling says must not be inferred.
func TestResolveNamesOneUnitOrSaysWhyItCannot(t *testing.T) {
	units := []unit{
		{name: "TestSignZero", file: "sign/sign_test.go", pkg: "sign"},
		{name: "TestShapeOnly", file: "shape/shape_test.go", pkg: "shape"},
		{name: "TestBoth", file: "sign/sign_test.go", pkg: "sign"},
		{name: "TestBoth", file: "shape/shape_test.go", pkg: "shape"},
		{name: "TestNotRun", file: "gone/gone_test.go", pkg: "gone"},
	}
	skipped := map[string]bool{"gone/gone_test.go": true}

	// Resolved: the one unit with that name.
	got, err := resolve("TestSignZero", units, skipped)
	if err != nil || got.file != "sign/sign_test.go" {
		t.Fatalf("resolve(TestSignZero) = %+v, %v", got, err)
	}
	// Resolved by qualification, which is what a caller does instead of guessing.
	got, err = resolve("shape/shape_test.go:TestBoth", units, skipped)
	if err != nil || got.file != "shape/shape_test.go" {
		t.Fatalf("resolve(qualified) = %+v, %v", got, err)
	}
	for _, tc := range []struct{ name, want string }{
		{"TestNoSuchThing", "names no test"},
		{"TestBoth", "ambiguous"},
		{"TestNotRun", "could not be run"},
		{"sign/sign_test.go:TestShapeOnly", "names no test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.name, units, skipped)
			if err == nil {
				t.Fatalf("resolve(%q) = %+v, want an error", tc.name, got)
			}
			if !errors.Is(err, ErrTestNotRun) {
				t.Errorf("resolve(%q) error is not ErrTestNotRun: %v", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("resolve(%q) = %v, want it to say %q", tc.name, err, tc.want)
			}
		})
	}
}

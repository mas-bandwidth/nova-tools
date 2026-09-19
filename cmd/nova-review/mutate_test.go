package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The verb's own surface: the one MUTATE line, the exit codes SPEC.md's Conventions give
// (0 passed, 1 ran and said no, 2 could not run), and the refusal a harvest keys on.

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mutateLab is a repo with a base commit and a head that fixes Sign at zero. redTest
// chooses whether the head's new test is red without the fix or green with or without it.
func mutateLab(t *testing.T, redTest bool) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "fixture@example.com")
	gitRun(t, dir, "config", "user.name", "Fixture")
	put(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\treturn -1\n}\n")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "base")
	gitRun(t, dir, "checkout", "-q", "-b", "fix")
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn -1\n}\n")
	newTest := "func TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n"
	if !redTest {
		newTest = "func TestSignBig(t *testing.T) {\n\tif Sign(99) != 1 {\n\t\tt.Fatal(\"big\")\n\t}\n}\n"
	}
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n\n"+newTest)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "fix with test")
	return dir
}

func head8(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))[:8]
}

func TestMutateVerbPrintsOneLineAndExitsZeroWhenTheTestIsRed(t *testing.T) {
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	want := "MUTATE " + head8(t, dir) + " reverted=1 red=1 green=1 PASS\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q (stderr %q)", out.String(), want, errb.String())
	}
}

// The same range, judged with GOFLAGS=-json in the environment the verb was
// started in. CI's `make test` exports exactly that, and on 2026-09-18 the
// inner `go test` inherited it: the run came back as a JSON stream with no
// `--- PASS:` line in it, the parser counted the green run as red, and
// `red=1 green=1` was reported as `red=2 green=0` on three legs of
// integration-4. The verb's answer is a property of the range, never of the
// caller's environment.
func TestMutateVerbIgnoresTheCallersGOFLAGS(t *testing.T) {
	t.Setenv("GOFLAGS", "-json")
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	want := "MUTATE " + head8(t, dir) + " reverted=1 red=1 green=1 PASS\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q (stderr %q)", out.String(), want, errb.String())
	}
}

func TestMutateVerbExitsOneAndNamesTheGreenTest(t *testing.T) {
	dir := mutateLab(t, false)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "MUTATE GREEN test=TestSignBig file=sign/sign_test.go") {
		t.Fatalf("the FAIL does not name the test that stayed green:\n%s", out.String())
	}
	if !strings.HasSuffix(errb.String(), "reverted=1 red=0 green=2 FAIL\n") {
		t.Fatalf("stderr = %q, want the FAIL verdict line", errb.String())
	}
}

// The listing is capped and counted like every listing here, and the remedy is the command
// that shows the rest.
func TestMutateGreenListingIsBounded(t *testing.T) {
	dir := mutateLab(t, false)
	var out, errb bytes.Buffer
	if code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD", "--max", "1"}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1 (stderr %s)", code, errb.String())
	}
	if strings.Count(out.String(), "MUTATE GREEN") != 1 {
		t.Fatalf("the cap did not hold:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "MUTATE MORE kind=green shown=1 total=2 nova-review mutate --repo") ||
		!strings.Contains(out.String(), "--max 0") {
		t.Fatalf("the MORE line does not carry the count and the remedy:\n%s", out.String())
	}
}

func TestMutateVerbRefusesARangeWithNoTestChange(t *testing.T) {
	dir := mutateLab(t, true)
	// Drop the test change: the head now carries only the fix.
	gitRun(t, dir, "checkout", "-q", "main", "--", "sign/sign_test.go")
	gitRun(t, dir, "commit", "-q", "-m", "drop the test")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE "+head8(t, dir)+" no-tests-changed\n" {
		t.Fatalf("stderr = %q, want the no-tests-changed refusal", errb.String())
	}
	if out.String() != "" {
		t.Fatalf("a refusal printed to stdout: %q", out.String())
	}
}

// A test-only range ABSTAINS rather than refusing, on stdout, still exit 2 (#1850,
// Stella's ruling stella-e72bbf88a3f7): reverting nothing runs the head's own suite,
// so the control cannot be PROVED -- which is not the same as a run that broke, and
// is not acceptance either.
func TestMutateVerbAbstainsOnATestOnlyRange(t *testing.T) {
	dir := mutateLab(t, true)
	gitRun(t, dir, "checkout", "-q", "main", "--", "sign/sign.go")
	gitRun(t, dir, "commit", "-q", "-m", "drop the fix, keep the test")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.HasPrefix(out.String(), "MUTATE ") || !strings.Contains(out.String(), "ABSTAIN reason=no-change-to-revert") {
		t.Fatalf("stdout = %q, want the typed ABSTAIN line", out.String())
	}
	if errb.String() != "" {
		t.Fatalf("stderr = %q, want the abstain on stdout alone", errb.String())
	}
}

func TestMutateVerbRefusesBadInvocation(t *testing.T) {
	for _, args := range [][]string{
		{"mutate", "--base", "main", "--head", "HEAD"},
		{"mutate", "--repo", ".", "--head", "HEAD"},
		{"mutate", "--repo", ".", "--base", "main", "--head", "HEAD", "--timeout", "0"},
		{"mutate", "--repo", ".", "--base", "main", "--head", "HEAD", "--max", "-1"},
		{"mutate", "--repo", ".", "--base", "main", "--head", "HEAD", "extra"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
		if !strings.HasPrefix(errb.String(), "MUTATE REFUSED: ") {
			t.Fatalf("%v: stderr = %q", args, errb.String())
		}
	}
}

func TestMutateVerbRefusesARepoThatIsNotAWorkingCopy(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"mutate", "--repo", t.TempDir(), "--base", "main", "--head", "HEAD"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %s)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "is not a git working copy") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestMutateUsageNamesTheVerb(t *testing.T) {
	if !strings.Contains(usage, "nova-review mutate --repo <dir> --base <ref> --head <ref>") {
		t.Fatalf("the help does not carry the mutate line:\n%s", usage)
	}
}

// ---------------------------------------------------------------------------
// SPEC-TOOLWORK.md §1 rule 9 (PR #1637), issue #1646 (T01).
//
// Two additions and nothing else: the range form says how much it reverted, and a
// seed form applies ONE edit and asserts that it was one. Every negative control in
// the gate is a seed, so a seed whose size nobody counted is a control that proves
// nothing: a seed that changed nothing proves the gate red on nothing, and a seed
// that changed two things does not say which one the gate caught.

// mutate-prints-reverted-count: "every non-test hunk" becomes a number a caller can
// gate on, rather than a sentence in a spec.
func TestMutatePrintsRevertedCount(t *testing.T) {
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	// One non-test file changed, one hunk in it: the fix's own hunk, put back.
	want := "MUTATE " + head8(t, dir) + " reverted=1 red=1 green=1 PASS\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q (stderr %q)", out.String(), want, errb.String())
	}
}

// seedLab is a repo at one commit whose sign package is already fixed and whose suite
// already covers the zero case. A seed is a mutant put INTO that head: the suite must
// catch it.
func seedLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "fixture@example.com")
	gitRun(t, dir, "config", "user.name", "Fixture")
	put(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn -1\n}\n")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "fixed, with the zero case covered")
	return dir
}

func seedFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seed.patch")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// One changed line: the mutant makes Sign(0) answer 1, and TestSignZero kills it.
const oneEditSeed = `--- a/sign/sign.go
+++ b/sign/sign.go
@@ -6,6 +6,6 @@ func Sign(n int) int {
 	}
 	if n == 0 {
-		return 0
+		return 1
 	}
 	return -1
 }
`

func TestMutateSeedPassesWhenTheSeedTurnsThePackageRed(t *testing.T) {
	dir := seedLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.HasPrefix(got, "MUTATE "+head8(t, dir)+" seed=") || !strings.HasSuffix(got, " edits=1 red=1 green=0 PASS\n") {
		t.Fatalf("stdout = %q, want the seed verdict line", got)
	}
}

// A mutant the suite does not kill is a FAIL, and it exits 1: the verb ran and said no.
func TestMutateSeedFailsWhenTheSeedSurvives(t *testing.T) {
	dir := seedLab(t)
	// Nothing in the suite reads the negative branch, so this mutant survives.
	survivor := `--- a/sign/sign.go
+++ b/sign/sign.go
@@ -9,3 +9,3 @@ func Sign(n int) int {
 	}
-	return -1
+	return -2
 }
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, survivor), "--tests", "sign"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.HasSuffix(errb.String(), " edits=1 red=0 green=1 FAIL\n") {
		t.Fatalf("stderr = %q, want the FAIL verdict line", errb.String())
	}
}

// mutate-seed-refuses-two-edits.
func TestMutateSeedRefusesTwoEdits(t *testing.T) {
	dir := seedLab(t)
	two := `--- a/sign/sign.go
+++ b/sign/sign.go
@@ -3,9 +3,9 @@ package sign
 func Sign(n int) int {
 	if n > 0 {
-		return 1
+		return 2
 	}
 	if n == 0 {
-		return 0
+		return 1
 	}
 	return -1
 }
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, two), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE REFUSED: seed makes 2 edits, want exactly 1\n" {
		t.Fatalf("stderr = %q, want the two-edit refusal", errb.String())
	}
}

// mutate-seed-refuses-zero-edits: a seed that changed nothing proves the gate red on
// nothing, so it is refused rather than reported as a control that held.
//
// This is also where the count proves it came from the WORKTREE and not from the
// patch file: read as a patch this is one removed line and one added line, which any
// arithmetic on the file itself calls one edit. Applied, it changes nothing. A gate
// that counted the patch would run this and report a suite proved against a defect
// that was never in the tree.
func TestMutateSeedRefusesZeroEdits(t *testing.T) {
	dir := seedLab(t)
	none := `--- a/sign/sign.go
+++ b/sign/sign.go
@@ -6,6 +6,6 @@ func Sign(n int) int {
 	}
 	if n == 0 {
-		return 0
+		return 0
 	}
 	return -1
 }
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, none), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE REFUSED: seed makes 0 edits, want exactly 1\n" {
		t.Fatalf("stderr = %q, want the zero-edit refusal", errb.String())
	}
}

// One line moved -- the same text removed in one place and added in another -- is one
// edit, and the gate's `renamed` and `no-test` style seeds depend on it being one.
func TestMutateSeedCountsAMovedLineAsOneEdit(t *testing.T) {
	dir := seedLab(t)
	moved := `--- a/sign/sign_test.go
+++ b/sign/sign_test.go
@@ -1,9 +1,9 @@
 package sign
 
-import "testing"
 
+import "testing"
 func TestSignZero(t *testing.T) {
 	if Sign(0) != 0 {
 		t.Fatal("zero")
 	}
 }
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, moved), "--tests", "sign"}, &out, &errb)
	if code == 2 {
		t.Fatalf("a moved line was refused as a bad count: %s", errb.String())
	}
	if !strings.Contains(out.String()+errb.String(), " edits=1 ") {
		t.Fatalf("out=%q err=%q, want edits=1 for one moved line", out.String(), errb.String())
	}
}

// The seed form keeps the range form's promise: it writes nothing into the repo it is
// pointed at. The mutant lives and dies in the throwaway worktree.
func TestMutateSeedWritesNothingIntoTheRepo(t *testing.T) {
	dir := seedLab(t)
	before := head8(t, dir)
	var out, errb bytes.Buffer
	run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign"}, &out, &errb)
	if head8(t, dir) != before {
		t.Fatalf("the head moved: %s -> %s", before, head8(t, dir))
	}
	body, err := os.ReadFile(filepath.Join(dir, "sign", "sign.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "return 1\n\t}\n\treturn -1") {
		t.Fatalf("the seed reached the caller's working copy:\n%s", body)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	st, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(st)) != "" {
		t.Fatalf("the repo is dirty after a seed run:\n%s", st)
	}
}

// A seed that does not apply at all is a could-not-run, not a verdict: the control was
// never installed, so nothing was proved either way.
func TestMutateSeedRefusesAPatchThatDoesNotApply(t *testing.T) {
	dir := seedLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, "--- a/nope.go\n+++ b/nope.go\n@@ -1 +1 @@\n-a\n+b\n"), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.HasPrefix(errb.String(), "MUTATE REFUSED: ") {
		t.Fatalf("stderr = %q, want a refusal", errb.String())
	}
}

// The two forms are exclusive, and each names what it needs: --seed wants --tests and
// no --base, the range wants --base and no --tests.
func TestMutateSeedFlagCombinationsAreRefused(t *testing.T) {
	dir := seedLab(t)
	seed := seedFile(t, oneEditSeed)
	for _, args := range [][]string{
		{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seed},
		{"mutate", "--repo", dir, "--head", "HEAD", "--tests", "sign"},
		{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD", "--seed", seed, "--tests", "sign"},
		{"mutate", "--repo", dir, "--seed", seed, "--tests", "sign"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (stdout %q stderr %q)", args, code, out.String(), errb.String())
		}
		if !strings.HasPrefix(errb.String(), "MUTATE REFUSED: ") {
			t.Fatalf("%v: stderr = %q, want a refusal", args, errb.String())
		}
	}
}

// ---------------------------------------------------------------------------
// The cold read of 2026-09-19 (#1708, findings 1-4). Every one of these is a way a
// seeded run reported PASS over a control that never ran: a typo in --tests, a
// deadline, a suite that was already red, and a count taken off lines that only look
// like file headers. A control that cannot fail is worse than no control, because a
// gate quotes it.

// slowSeedLab is seedLab with one unit that outlasts any deadline a caller would set,
// so the run is killed rather than answered. The sleep is long enough that the unit
// cannot finish before the deadline on a loaded bench, and the test costs the DEADLINE,
// never the sleep.
func slowSeedLab(t *testing.T) string {
	t.Helper()
	dir := seedLab(t)
	put(t, dir, "sign/slow_test.go", "package sign\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSignSlow(t *testing.T) {\n\ttime.Sleep(9 * time.Second)\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "a unit that outlasts the deadline")
	return dir
}

// mutate-seed-refuses-a-package-that-does-not-exist: `go test ./nosuch/` exits
// non-zero with no FAIL line, and a run that counted that as a kill reported
// "edits=1 red=1 green=0 PASS" for a package name nobody spelled right.
func TestMutateSeedRefusesAPackageThatDoesNotExist(t *testing.T) {
	dir := seedLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "nosuch"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if out.String() != "" {
		t.Fatalf("a verdict printed for a package that does not exist: %q", out.String())
	}
	// Named for what it is -- nothing at this head resolves under that name -- and
	// not as whatever `go test` happened to do with the name afterwards.
	if !strings.HasPrefix(errb.String(), "MUTATE REFUSED: ") ||
		!strings.Contains(errb.String(), "nosuch") ||
		!strings.Contains(errb.String(), "not a package at this head") {
		t.Fatalf("stderr = %q, want a refusal naming the package", errb.String())
	}
}

// mutate-seed-timeout-is-not-a-pass: the deadline kills `go test` before any unit
// reports, and a deadline is a could-not-run -- exit 2 -- never a mutant that died.
//
// This is the end-to-end of it, and the only way to have it end to end is to let a
// real second pass: the deadline is `--timeout`, in whole seconds, on the wall clock.
// So it is behind `-short`, and `internal/review`'s
// TestSeedTimeoutIsACouldNotRunAndNeverAKill holds the same rule at the function that
// decides it, instantly, on every run. The two-minute law is not a thing to spend a
// second of on a branch that already has the proof.
func TestMutateSeedTimeoutIsNotAPass(t *testing.T) {
	if testing.Short() {
		t.Skip("a real --timeout is a real second; internal/review holds this rule without the clock")
	}
	dir := slowSeedLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign", "--timeout", "1"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if out.String() != "" {
		t.Fatalf("a verdict printed for a run that was killed: %q", out.String())
	}
	if !strings.Contains(errb.String(), "deadline") {
		t.Fatalf("stderr = %q, want a refusal naming the deadline", errb.String())
	}
}

// mutate-seed-refuses-a-suite-already-red: a suite red at the unseeded head kills
// every seed, so a PASS under it is the head's own failure wearing the control's name.
func TestMutateSeedRefusesASuiteAlreadyRedAtTheHead(t *testing.T) {
	dir := seedLab(t)
	// The unit now asserts something the fixed code does not do: red before any seed.
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 7 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "a suite that is already red")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if out.String() != "" {
		t.Fatalf("a verdict printed over a suite that was already red: %q", out.String())
	}
	if !strings.HasPrefix(errb.String(), "MUTATE REFUSED: ") || !strings.Contains(errb.String(), "already red") {
		t.Fatalf("stderr = %q, want a refusal naming the red baseline", errb.String())
	}
}

// mutate-seed-counts-a-content-line-that-looks-like-a-file-header: one removed
// Markdown rule is one edit. Read line by line, `----` in the applied diff is a `---`
// file header, and the seed was refused as a control that changed nothing.
func TestMutateSeedCountsAContentLineThatLooksLikeAFileHeader(t *testing.T) {
	dir := seedLab(t)
	put(t, dir, "sign/doc.md", "title\n---\nbody\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "a note whose second line is a rule")
	removesARule := `--- a/sign/doc.md
+++ b/sign/doc.md
@@ -1,3 +1,2 @@
 title
----
 body
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, removesARule), "--tests", "sign"}, &out, &errb)
	if code == 2 {
		t.Fatalf("one removed line was refused as a bad count: %s", errb.String())
	}
	if !strings.Contains(out.String()+errb.String(), " edits=1 ") {
		t.Fatalf("out=%q err=%q, want edits=1 for one removed line", out.String(), errb.String())
	}
}

// A package-level FAIL with no `--- FAIL:` line -- a guard in TestMain that exits the
// binary itself -- is still the mutant dying. The refusals above narrow what counts as
// a kill; this is the line they must not cross.
func TestMutateSeedCountsAPackageLevelFailAsAKill(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "fixture@example.com")
	gitRun(t, dir, "config", "user.name", "Fixture")
	put(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn -1\n}\n")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) {\n\tm.Run()\n\tif Sign(0) != 0 {\n\t\tos.Exit(1)\n\t}\n\tos.Exit(0)\n}\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "a package whose guard exits the binary")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.HasSuffix(out.String(), " edits=1 red=1 green=1 PASS\n") {
		t.Fatalf("stdout = %q, want the package-level FAIL counted as one kill", out.String())
	}
}

// ---------------------------------------------------------------------------
// The selected form: `--test <name>` (issue #1849, Stella's ruling
// stella-e72bbf88a3f7). The readers' receipt is PR #1828: the card's named TEST:
// goes red with the change reverted and a co-touched test in another file stays
// green -- correctly, it compares line prefixes and never prose -- and the per-FILE
// verdict said FAIL for a fix SPEC-TOOLWORK §1 rule 4(d) would accept.
//
// The ruling, and what each test below holds: the default per-file behaviour is
// unchanged; the selected form answers only whether THAT unit detects the reverted
// change; absent, ambiguous, unchanged-file-only and unexecuted are REFUSED and never
// a vacuous PASS or an inferred FAIL; the verdict carries test=<resolved name>; and
// every remedy carries --test so a rerun asks the same question.

// selectLab is the #1828 shape: one fix, two changed test files, and the second
// file's test is insensitive to it -- green with the change and green without.
func selectLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "fixture@example.com")
	gitRun(t, dir, "config", "user.name", "Fixture")
	put(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\treturn -1\n}\n")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n")
	put(t, dir, "shape/shape_test.go", "package shape\n\nimport \"testing\"\n\nfunc TestShapeOnly(t *testing.T) {\n\tif len(\"MUTATE\") != 6 {\n\t\tt.Fatal(\"shape\")\n\t}\n}\n")
	// A test file the range does NOT change: it exists at the base and at the head,
	// and --test may not reach it, because the question is about this range.
	put(t, dir, "other/other_test.go", "package other\n\nimport \"testing\"\n\nfunc TestElsewhere(t *testing.T) {}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "base")
	gitRun(t, dir, "checkout", "-q", "-b", "fix")
	// The fix, and the test that detects it.
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn -1\n}\n")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	// The co-touched test file, strengthened in a way the fix cannot affect.
	put(t, dir, "shape/shape_test.go", "package shape\n\nimport \"testing\"\n\nfunc TestShapeOnly(t *testing.T) {\n\tif len(\"MUTATE\") != 6 {\n\t\tt.Fatal(\"shape\")\n\t}\n\tif len(\"MUTATE SKIP\") != 11 {\n\t\tt.Fatal(\"shape two\")\n\t}\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "the fix, its test, and a co-touched insensitive one")
	return dir
}

// The default is UNCHANGED, and this is the receipt the ruling is built on: the
// per-file rule says FAIL because `shape` has no red unit, though the card's named
// test is red. It is the control for the whole slice -- if this ever passes by
// itself, --test proved nothing.
func TestMutateRangeStillFailsPerFileWithoutTheFlag(t *testing.T) {
	dir := selectLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1 (the per-file rule, unchanged)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), " FAIL\n") {
		t.Fatalf("stderr = %q, want the per-file FAIL verdict", errb.String())
	}
	// The GREEN listing has always carried test= per line; the VERDICT must not.
	if strings.Contains(errb.String(), "test=") {
		t.Fatalf("the default verdict carries a test= field: %q", errb.String())
	}
}

// #1849: the selected form answers rule 4(d)'s question and PASSes the same range.
func TestMutateSelectedTestPassesWhenTheNamedUnitIsRed(t *testing.T) {
	dir := selectLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD", "--test", "TestSignZero"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), " test=TestSignZero ") {
		t.Fatalf("stdout = %q, want the resolved name on the verdict line", out.String())
	}
	if !strings.HasSuffix(out.String(), " PASS\n") {
		t.Fatalf("stdout = %q, want PASS", out.String())
	}
	// The evidence stays: a verdict with no counts is a verdict nobody can check.
	for _, field := range []string{"reverted=", "red=", "green="} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("stdout = %q, want the %s evidence retained", out.String(), field)
		}
	}
}

// The other half of the ruling: a named unit that RAN GREEN is FAIL, exit 1 -- the
// question was asked and answered no. Another test's red must not answer it.
func TestMutateSelectedTestFailsWhenTheNamedUnitRanGreen(t *testing.T) {
	dir := selectLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD", "--test", "TestShapeOnly"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), " test=TestShapeOnly ") || !strings.HasSuffix(errb.String(), " FAIL\n") {
		t.Fatalf("stderr = %q, want the selected FAIL verdict naming the unit", errb.String())
	}
}

// Absent, ambiguous and unchanged-file-only are REFUSED, never a vacuous PASS and
// never an inferred FAIL. "Do not guess which test the caller meant."
func TestMutateSelectedTestRefusesWhenItCannotBeResolved(t *testing.T) {
	dir := selectLab(t)
	// selectLab already carries other/other_test.go at the BASE, unchanged by this
	// range: a name in the repo but in no CHANGED test file cannot be asked, and
	// answering about a different unit would be the wrong answer.
	gitRun(t, dir, "checkout", "-q", "-b", "ambiguous")
	// The same NAME in two changed test files.
	put(t, dir, "shape/shape_test.go", "package shape\n\nimport \"testing\"\n\nfunc TestShapeOnly(t *testing.T) {}\n\nfunc TestSignZero(t *testing.T) {}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "the same test name in a second changed file")
	for _, tc := range []struct {
		name string
		head string
		test string
		want string
	}{
		{"a name nothing declares", "fix", "TestNoSuchThing", "names no test"},
		{"a name in no changed test file", "master", "TestElsewhere", "names no test"},
		{"a name two changed test files declare", "ambiguous", "TestSignZero", "ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := tc.head
			if head == "master" {
				head = "fix"
			}
			var out, errb bytes.Buffer
			code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", head, "--test", tc.test}, &out, &errb)
			if code != 2 {
				t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
			}
			if !strings.HasPrefix(errb.String(), "MUTATE REFUSED: ") || !strings.Contains(errb.String(), tc.want) {
				t.Fatalf("stderr = %q, want a refusal saying %q", errb.String(), tc.want)
			}
			if strings.Contains(out.String()+errb.String(), " PASS") || strings.Contains(out.String()+errb.String(), " FAIL") {
				t.Fatalf("a verdict was printed for a name that could not be resolved: %q %q", out.String(), errb.String())
			}
		})
	}
}

// The two forms stay distinct: seed's plural --tests is a package selector, the
// range's singular --test is a unit. Together they are malformed.
func TestMutateSelectedTestIsRangeOnly(t *testing.T) {
	dir := selectLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, oneEditSeed), "--tests", "sign", "--test", "TestSignZero"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "--test") {
		t.Fatalf("stderr = %q, want the refusal to name --test", errb.String())
	}
}

// "Any MORE/remedy must carry --test so rerunning asks the same question." A remedy
// that dropped it would print the per-file listing for a different question.
func TestMutateSelectedRemedyCarriesTheFlag(t *testing.T) {
	dir := selectLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD", "--test", "TestShapeOnly", "--max", "1"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	var more string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "MUTATE MORE ") {
			more = line
		}
	}
	if more == "" {
		t.Fatalf("no MORE line to check in:\n%s", out.String())
	}
	if !strings.Contains(more, "--test") || !strings.Contains(more, "TestShapeOnly") {
		t.Fatalf("the MORE line's remedy drops --test: %q", more)
	}
}

// ---------------------------------------------------------------------------
// #1850, Stella's ruling: `no-change-to-revert` becomes a typed ABSTAIN on stdout,
// still exit 2 -- inability to prove the control, and NEVER acceptance. The
// `no-tests-changed` refusal is unchanged: a production fix with no test is not
// automatically harmless.

// The readers' receipt is PR #1812: every changed file is a test file.
func TestMutateAbstainsWhenThereIsNoChangeToRevert(t *testing.T) {
	dir := mutateLab(t, true)
	gitRun(t, dir, "checkout", "-q", "-b", "tests-only")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n\nfunc TestSignNegative(t *testing.T) {\n\tif Sign(-2) != -1 {\n\t\tt.Fatal(\"negative\")\n\t}\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "a test-only change")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "fix", "--head", "HEAD"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2: an abstain is inability to prove the control, not acceptance\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	want := "MUTATE " + head8(t, dir) + " ABSTAIN reason=no-change-to-revert"
	if !strings.HasPrefix(out.String(), want) {
		t.Fatalf("stdout = %q, want the typed ABSTAIN line on STDOUT beginning %q", out.String(), want)
	}
	if strings.Contains(out.String()+errb.String(), " PASS") || strings.Contains(out.String()+errb.String(), " FAIL") {
		t.Fatalf("an abstain printed a verdict: %q %q", out.String(), errb.String())
	}
	if strings.Contains(errb.String(), "ABSTAIN") {
		t.Fatalf("the ABSTAIN line went to stderr as well: %q", errb.String())
	}
}

// The condition the ruling deliberately did NOT move: a range with no changed test
// file is still a refusal, because it can be an ordinary production fix missing the
// red test it was required to have.
func TestMutateStillRefusesWhenNoTestFileChanged(t *testing.T) {
	dir := mutateLab(t, true)
	gitRun(t, dir, "checkout", "-q", "-b", "prod-only")
	put(t, dir, "sign/sign.go", "package sign\n\n// a comment-only change to production code\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn -1\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "production only")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "fix", "--head", "HEAD"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE "+head8(t, dir)+" no-tests-changed\n" {
		t.Fatalf("stderr = %q, want the no-tests-changed refusal, unchanged", errb.String())
	}
	if strings.Contains(out.String(), "ABSTAIN") {
		t.Fatalf("no-tests-changed was turned into an abstain: %q", out.String())
	}
}

// A mixed range keeps ordinary PASS/FAIL, which is what stops the abstain widening
// into "any range with a test file in it".
func TestMutateMixedRangeKeepsItsVerdict(t *testing.T) {
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	if code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.HasSuffix(out.String(), " PASS\n") || strings.Contains(out.String(), "ABSTAIN") {
		t.Fatalf("stdout = %q, want an ordinary PASS", out.String())
	}
}

// The mirror of the ruling's "another test's green result must not answer the
// selected question": another test's RED must not answer it either. Here the
// default per-file verdict is PASS -- the one changed test file has a red unit --
// and the selected unit is the file's OTHER test, which ran and stayed green. The
// selected form must say FAIL, or naming a test would be a way to borrow a passing
// verdict from the test next to it.
func TestMutateSelectedTestIsNotAnsweredByItsFileNeighbour(t *testing.T) {
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	if code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb); code != 0 {
		t.Fatalf("the default verdict is not PASS, so this proves nothing: exit %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD", "--test", "TestSignPositive"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1: the named unit ran green\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), " test=TestSignPositive ") || !strings.HasSuffix(errb.String(), " FAIL\n") {
		t.Fatalf("stderr = %q, want the selected FAIL naming the unit", errb.String())
	}
}

// ---------------------------------------------------------------------------
// Emma's bench dogfood of `mutate --seed`, 2026-09-19 (issue #1803).
//
// The count was `max(total added, total removed)` over the WHOLE applied patch, so
// two edits in two PLACES cancelled into one: a line added here and a line removed
// there is added=1, removed=1, and the larger of the two is 1. Both receipts below
// are hers, and both printed `edits=1 red=1 green=0 PASS` before this was fixed --
// a control that proved a suite against two defects at once and named neither.
//
// SPEC-TOOLWORK.md §1 rule 7: "refused unless it changes exactly one line -- one `-`
// and one `+`, or one added line, or one removed line, or one line moved".

// #1803 receipt 1: one line ADDED in one file and one line REMOVED in another.
func TestMutateSeedRefusesTwoFiles(t *testing.T) {
	dir := seedLab(t)
	twoFiles := `--- a/sign/sign_test.go
+++ b/sign/sign_test.go
@@ -4,2 +4,3 @@ package sign
 
+// an added line, in the first file
 func TestSignZero(t *testing.T) {
--- a/sign/sign.go
+++ b/sign/sign.go
@@ -4,3 +4,2 @@ func Sign(n int) int {
 	if n > 0 {
-		return 1
 	}
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, twoFiles), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE REFUSED: seed makes 2 edits, want exactly 1\n" {
		t.Fatalf("stderr = %q, want the two-edit refusal for a two-FILE seed", errb.String())
	}
}

// #1803 receipt 2: one line ADDED in one function and one line REMOVED in another,
// in the SAME file -- two hunks, two places, and not a moved line.
func TestMutateSeedRefusesTwoHunksInOneFile(t *testing.T) {
	dir := seedLab(t)
	twoHunks := `--- a/sign/sign.go
+++ b/sign/sign.go
@@ -4,3 +4,2 @@ func Sign(n int) int {
 	if n > 0 {
-		return 1
 	}
@@ -9,2 +8,3 @@ func Sign(n int) int {
 	}
+	// a second edit, somewhere else entirely
 	return -1
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, twoHunks), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE REFUSED: seed makes 2 edits, want exactly 1\n" {
		t.Fatalf("stderr = %q, want the two-edit refusal for a two-HUNK seed", errb.String())
	}
}

// The moved-line exception is rule 7's own and it stops at the file. The same text
// out of one file and into another is the one shape where the per-PLACE count and
// the "one line moved" exception disagree, and two files is two places: a gate that
// went red under it has caught something about `a.go` or something about `b.go`.
func TestMutateSeedRefusesALineMovedBetweenFiles(t *testing.T) {
	dir := seedLab(t)
	movedAcross := `--- a/sign/sign.go
+++ b/sign/sign.go
@@ -4,3 +4,2 @@ func Sign(n int) int {
 	if n > 0 {
-		return 1
 	}
--- a/sign/sign_test.go
+++ b/sign/sign_test.go
@@ -4,2 +4,3 @@ package sign
 
+		return 1
 func TestSignZero(t *testing.T) {
`
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--head", "HEAD", "--seed", seedFile(t, movedAcross), "--tests", "sign"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if errb.String() != "MUTATE REFUSED: seed makes 2 edits, want exactly 1\n" {
		t.Fatalf("stderr = %q, want the two-edit refusal for a line moved BETWEEN files", errb.String())
	}
}

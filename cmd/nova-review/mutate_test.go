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
	want := "MUTATE " + head8(t, dir) + " red=1 green=1 PASS\n"
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
	want := "MUTATE " + head8(t, dir) + " red=1 green=1 PASS\n"
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
	if !strings.HasSuffix(errb.String(), "red=0 green=2 FAIL\n") {
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

func TestMutateVerbRefusesATestOnlyRange(t *testing.T) {
	dir := mutateLab(t, true)
	gitRun(t, dir, "checkout", "-q", "main", "--", "sign/sign.go")
	gitRun(t, dir, "commit", "-q", "-m", "drop the fix, keep the test")
	var out, errb bytes.Buffer
	code := run([]string{"mutate", "--repo", dir, "--base", "main", "--head", "HEAD"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "no-change-to-revert") {
		t.Fatalf("stderr = %q, want the no-change-to-revert refusal", errb.String())
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

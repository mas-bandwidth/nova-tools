package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #2042: the guard verdict is COMPUTED from exit codes and test names, never judged.
// Revert the commit's non-test files, keep the tests, run the named packages.

func TestGuardUsageNamesTheVerb(t *testing.T) {
	if !strings.Contains(usage, "nova-review guard --repo <dir> --head <ref>") {
		t.Fatalf("the help does not carry the guard line:\n%s", usage)
	}
}

func TestGuardVerbComputesGuardedWhenNamedTestsGoRed(t *testing.T) {
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0 (GUARDED)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !hasVerdict(got, "GUARDED") {
		t.Fatalf("the control went red and the verdict must be GUARDED, got:\nstdout:%s\nstderr:%s", out.String(), errb.String())
	}
	if !strings.Contains(got, "platform="+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("the line must record the platform, got:\n%s", got)
	}
	if hasVerdict(got, "UNGUARDED") {
		t.Fatalf("a red control must not print UNGUARDED:\n%s", got)
	}
}

func TestGuardVerbComputesUnguardedWhenTestsStayGreen(t *testing.T) {
	dir := mutateLab(t, false)
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1 (UNGUARDED)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !hasVerdict(got, "UNGUARDED") {
		t.Fatalf("the control stayed green and the verdict must be UNGUARDED, got:\nstdout:%s\nstderr:%s", out.String(), errb.String())
	}
	if hasVerdict(got, "GUARDED") {
		t.Fatalf("a green control must not print GUARDED:\n%s", got)
	}
}

func TestGuardVerbIsNotApplicableWhenTheFileIsForAnotherGOOS(t *testing.T) {
	dir := otherGOOSLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2 (NOT-APPLICABLE)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !strings.Contains(got, "NOT-APPLICABLE") {
		t.Fatalf("a control that cannot compile the file on this OS is NOT-APPLICABLE, not UNGUARDED, got:\nstdout:%s\nstderr:%s", out.String(), errb.String())
	}
	if hasVerdict(got, "UNGUARDED") {
		t.Fatalf("a foreign-GOOS file must not be scored UNGUARDED on %s:\n%s", runtime.GOOS, got)
	}
	if !strings.Contains(got, "status=NOT-APPLICABLE") {
		t.Fatalf("N/A ends in reason=; the line must name status= so a card does not copy the last token:\n%s", got)
	}
}

func TestGuardVerbReportsCompilerHeldWhenRevertDoesNotCompile(t *testing.T) {
	dir := compilerHeldLab(t)
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1 (COMPILER-HELD)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !strings.Contains(got, "COMPILER-HELD") {
		t.Fatalf("a revert that only breaks the build is COMPILER-HELD, not GUARDED, got:\nstdout:%s\nstderr:%s", out.String(), errb.String())
	}
	if hasVerdict(got, "GUARDED") {
		t.Fatalf("compilation coupling is not an assertion:\n%s", got)
	}
}

func TestGuardVerbDoesNotReadAResultFile(t *testing.T) {
	dir := mutateLab(t, true)
	put(t, dir, "RESULT.md", "RESULT: card-tools20-guard\nUNGUARDED\n")
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	got := out.String() + errb.String()
	if code != 0 || !hasVerdict(got, "GUARDED") {
		t.Fatalf("the model's word in RESULT.md must not be the verdict (exit %d):\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if hasVerdict(got, "UNGUARDED") {
		t.Fatalf("RESULT.md said UNGUARDED and two named tests go red; the control must ignore the file:\n%s", got)
	}
}

func TestGuardVerbRecordsBothTails(t *testing.T) {
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !strings.Contains(got, "GUARD TAIL which=baseline") {
		t.Fatalf("missing baseline tail:\n%s", got)
	}
	if !strings.Contains(got, "GUARD TAIL which=control") {
		t.Fatalf("missing control tail:\n%s", got)
	}
}

func TestGuardVerbRefusesWithoutRepoAndHead(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"guard"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %s)", code, errb.String())
	}
	if !strings.HasPrefix(errb.String(), "GUARD REFUSED: ") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestGuardVerbIgnoresTheCallersGOFLAGS(t *testing.T) {
	t.Setenv("GOFLAGS", "-json")
	dir := mutateLab(t, true)
	var out, errb bytes.Buffer
	code := run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !hasVerdict(got, "GUARDED") {
		t.Fatalf("GOFLAGS=-json must not reshape the verdict:\n%s", got)
	}
}

// hasVerdict matches the named status= field: N/A and ABSTAIN end in reason/prose,
// so a last-token match would copy the wrong word.
func hasVerdict(got, verdict string) bool {
	want := "status=" + verdict
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "GUARD ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if f == want {
				return true
			}
		}
	}
	return false
}

// compilerHeldLab is a head that adds a symbol a test only CALLS. Reverting the
// code breaks the build; that is held by the compiler, not by an assertion.
func compilerHeldLab(t *testing.T) string {
	t.Helper()
	dir := mutateLab(t, true)
	put(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n > 0 {\n\t\treturn 1\n\t}\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn -1\n}\n\nfunc Mul(a, b int) int { return a * b }\n")
	put(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignPositive(t *testing.T) {\n\tif Sign(5) != 1 {\n\t\tt.Fatal(\"positive\")\n\t}\n}\n\nfunc TestMul(t *testing.T) {\n\t_ = Mul(2, 3)\n}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "call-only coupling")
	return dir
}

// otherGOOSLab is a head whose only production change is a file this OS will not
// compile. Scoring that UNGUARDED is the 7afd48c0 failure.
func otherGOOSLab(t *testing.T) string {
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
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	put(t, dir, "sign/sign_"+other+".go", "package sign\n\nfunc Extra() int { return 7 }\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "other-GOOS file")
	return dir
}

func TestGuardVerbDoesNotWriteTheCallersRepo(t *testing.T) {
	dir := mutateLab(t, true)
	before, err := os.ReadFile(filepath.Join(dir, "sign", "sign.go"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	_ = run([]string{"guard", "--repo", dir, "--head", "HEAD"}, &out, &errb)
	after, err := os.ReadFile(filepath.Join(dir, "sign", "sign.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("guard wrote the caller's repo; the revert belongs in a throwaway worktree")
	}
}

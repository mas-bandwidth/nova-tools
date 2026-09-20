package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func guardFixture(t *testing.T, dir string) (*GuardResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return Guard(ctx, GuardOptions{Repo: dir, Head: "HEAD", TempRoot: t.TempDir()})
}

func TestGuardIsGuardedWhenTheNewTestGoesRedWithoutTheChange(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fix")
	write(t, dir, "sign/sign.go", `package sign

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
		t.Fatal("zero")
	}
}
`)
	commit(t, dir, "fix with test")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictGuarded {
		t.Fatalf("verdict = %s, want GUARDED (red=%d green=%d tails=%+v)", res.Verdict, res.Red, res.Green, res.Tails)
	}
	if res.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("platform = %q", res.Platform)
	}
}

func TestGuardIsUnguardedWhenTestsStayGreen(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fix")
	write(t, dir, "sign/sign.go", `package sign

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
	commit(t, dir, "fix without a test of zero")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictUnguarded {
		t.Fatalf("verdict = %s, want UNGUARDED (red=%d green=%d)", res.Verdict, res.Red, res.Green)
	}
}

func TestGuardIsCompilerHeldWhenRevertDoesNotCompile(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "callonly")
	write(t, dir, "sign/sign.go", `package sign

func Sign(n int) int {
	if n > 0 {
		return 1
	}
	return -1
}

func Mul(a, b int) int { return a * b }
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestMul(t *testing.T) {
	_ = Mul(2, 3)
}
`)
	commit(t, dir, "call-only")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictCompilerHeld {
		t.Fatalf("verdict = %s, want COMPILER-HELD", res.Verdict)
	}
}

// Stella HOLD of #2128: a mixed commit (value.txt + a foreign-OS Go file) was
// scored NOT-APPLICABLE because anyProductionGoApplies skipped the non-Go path.
// Reverting value.txt still makes the retained test red. N/A only when every
// production path is excluded Go.
func TestGuardIsGuardedWhenANonGoFileIsRevertedBesideAnExcludedGoFile(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "sign/value.txt", "base\n")
	write(t, dir, "sign/sign_test.go", `package sign

import (
	"os"
	"testing"
)

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestValue(t *testing.T) {
	b, err := os.ReadFile("value.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "base\n" {
		t.Fatalf("value = %q", b)
	}
}
`)
	commit(t, dir, "value at base")
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	run(t, dir, "git", "checkout", "-q", "-b", "mixed")
	write(t, dir, "sign/value.txt", "head\n")
	write(t, dir, "sign/sign_"+other+".go", "package sign\n\nfunc Extra() int { return 7 }\n")
	write(t, dir, "sign/sign_test.go", `package sign

import (
	"os"
	"testing"
)

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestValue(t *testing.T) {
	b, err := os.ReadFile("value.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "head\n" {
		t.Fatalf("value = %q", b)
	}
}
`)
	commit(t, dir, "value.txt plus excluded Go")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictGuarded {
		t.Fatalf("mixed non-Go + excluded Go: verdict = %s, want GUARDED (reverting value.txt is still a control; N/A only when every production path is excluded Go)", res.Verdict)
	}
}

// Stella HOLD of 4edb857b: a deleted ordinary production .go was skipped in
// productionAppliesHere, so Guard returned NOT-APPLICABLE before the control ran.
// Restoring obsolete.go makes the retained regression red.
func TestGuardIsGuardedWhenProductionGoFileIsDeleted(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "delete")
	write(t, dir, "sign/obsolete.go", "package sign\n\nfunc Obsolete() int { return 1 }\n")
	commit(t, dir, "add obsolete")
	if err := os.Remove(filepath.Join(dir, "sign", "obsolete.go")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "sign/sign_test.go", `package sign

import (
	"os"
	"testing"
)

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestObsoleteGone(t *testing.T) {
	if _, err := os.Stat("obsolete.go"); err == nil {
		t.Fatal("obsolete.go still present")
	}
}
`)
	commit(t, dir, "delete obsolete")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictGuarded {
		t.Fatalf("deleted production Go: verdict = %s, want GUARDED (restoring obsolete.go makes retained regression red)", res.Verdict)
	}
}

func TestGuardIsNotApplicableForAForeignGOOSFile(t *testing.T) {
	dir := newRepo(t)
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	run(t, dir, "git", "checkout", "-q", "-b", "otheros")
	write(t, dir, "sign/sign_"+other+".go", "package sign\n\nfunc Extra() int { return 7 }\n")
	commit(t, dir, "other-GOOS file")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictNotApplicable {
		t.Fatalf("verdict = %s, want NOT-APPLICABLE (a control that cannot compile the file on %s is not UNGUARDED)", res.Verdict, runtime.GOOS)
	}
	if res.Reason != "build-tags" {
		t.Fatalf("reason = %q", res.Reason)
	}
}

func TestGuardAbstainsWhenEveryChangedFileIsATest(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "tests")
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

func TestSignNegative(t *testing.T) {
	if Sign(-3) != -1 {
		t.Fatal("negative")
	}
}
`)
	commit(t, dir, "tests only")
	_, err := guardFixture(t, dir)
	if !errors.Is(err, ErrNoChangeToRevert) {
		t.Fatalf("err = %v, want ErrNoChangeToRevert", err)
	}
}

func TestGuardDoesNotOpenAResultFile(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fix")
	write(t, dir, "sign/sign.go", `package sign

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
		t.Fatal("zero")
	}
}
`)
	write(t, dir, "RESULT.md", "UNGUARDED\n")
	commit(t, dir, "fix with a lying RESULT.md")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictGuarded {
		t.Fatalf("RESULT.md said UNGUARDED; the control must ignore it, got %s", res.Verdict)
	}
}

func TestGuardRecordsBothTails(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fix")
	write(t, dir, "sign/sign.go", `package sign

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
		t.Fatal("zero")
	}
}
`)
	commit(t, dir, "fix with test")
	res, err := guardFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	var baseline, control bool
	for _, tail := range res.Tails {
		if tail.Which == "baseline" {
			baseline = true
		}
		if tail.Which == "control" {
			control = true
		}
	}
	if !baseline || !control {
		t.Fatalf("missing tails: %+v", res.Tails)
	}
	if res.Red == 0 || !containsName(res.Reds, "TestSignZero") {
		t.Fatalf("the named test that went red was not recorded: reds=%v", res.Reds)
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want || strings.HasPrefix(n, want) {
			return true
		}
	}
	return false
}

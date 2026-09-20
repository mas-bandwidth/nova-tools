package review

// #1807 (the red team of T03 at 98e3f3a9, PROBE-B): the mutate control accepted a
// call-only test that asserts NOTHING, because reverting the fix broke compilation and a
// package that does not build was scored as every unit red. Compilation coupling is not
// an assertion: the card writes `_ = Mul(2, 3)`, the revert deletes Mul, the package
// fails to build, and `[build failed]` was read as the kill the control was looking for.
//
// A build failure on the reverted side is never a kill: the changed test file is skipped
// with the reason that says so and no unit in it is judged to have failed.

import (
	"strings"
	"testing"
)

func TestMutateSkipsACallOnlyTestCoupledByCompilation(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "callonly")
	write(t, dir, "sign/sign.go", `package sign

func Sign(n int) int {
	if n > 0 {
		return 1
	}
	return -1
}

// Mul is the fix's new symbol, and it is wrong.
func Mul(a, b int) int { return 0 }
`)
	write(t, dir, "sign/sign_test.go", `package sign

import "testing"

func TestSignPositive(t *testing.T) {
	if Sign(5) != 1 {
		t.Fatal("positive")
	}
}

// TestMul asserts nothing at all. It is coupled to the fix only by compiling against it.
func TestMul(t *testing.T) {
	_ = Mul(2, 3)
}
`)
	commit(t, dir, "a call-only test coupled to the fix by compilation")

	res, err := mutateFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("MUTATE PASS on a call-only test that asserts nothing: the revert broke the build and that was read as the kill (red=%d greens=%v reds=%v)", res.Red, res.Greens, res.Reds)
	}
	if res.Red != 0 {
		t.Errorf("a build failure on the reverted side was counted as %d kill(s); it is never a kill", res.Red)
	}
	if len(res.Skips) != 1 || !strings.HasPrefix(res.Skips[0].Reason, "revert-did-not-compile") {
		t.Errorf("the file whose package would not compile after the revert was not skipped with the reason that says so: skips=%+v", res.Skips)
	}
}

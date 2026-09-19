package pulse

// The red team's two escapes from the read of T03 at 98e3f3a9
// (tmp/session-0919b/redteam-launch/REPORT.md), each written here with the reader's own
// receipt as the test.
//
// #1806 (PROBE-A). `acceptToolchainRed` scored a wall marker printed BEFORE the first
// `=== RUN` as the bench's fault. A card's own TestMain or package init runs before any
// `=== RUN`, so a card could print `Operation not permitted`, fail, and be scored
// `ABSTAIN toolchain` instead of `REJECT red-at-head` -- dodging the rejection AND the
// track-record fail, and staling the bench's certification record on the way out.
//
// The rule now: output a process the CARD controls is never the bench's voice. When the
// card's code runs, the wall speaks through its exit code and through nothing else
// (nova-sandbox's 125, 126, 127). For `go build` and `go vet` the output is the
// toolchain's own and the text still counts, with the `# pkg` and `[build failed]`
// carve-outs that keep a compiler error naming WALL the card's.

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// exitErr is a real *exec.ExitError with the given code, the shape PROBE-A used: the
// marker text with an ordinary exit 1 under it.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("wanted a real *exec.ExitError for exit %d, got %v", code, err)
	}
	return ee
}

func TestAcceptAWallMarkerFromTheCardsOwnProcessIsNotTheBenchs(t *testing.T) {
	// PROBE-A, verbatim: the marker before any `=== RUN`, with a plain exit 1 under it.
	const beforeRun = "Operation not permitted\nFAIL\tgithub.com/x/y\t0.01s\n"
	const sandboxDenied = "SANDBOX DENIED path=/etc/shadow\n--- FAIL: TestX\nFAIL\tgithub.com/x/y\t0.01s\n"
	const afterRun = "=== RUN   TestX\n    x_test.go:9: Operation not permitted\n--- FAIL: TestX\nFAIL\tgithub.com/x/y\t0.01s\n"

	for _, tc := range []struct {
		name     string
		out      string
		cardRuns bool
		want     bool
		why      string
	}{
		{"marker before RUN, the card's process", beforeRun, true, false, "a card's TestMain can print anything; the bench speaks through the exit code"},
		{"SANDBOX DENIED from the card's process", sandboxDenied, true, false, "the wall's own refusal comes back as exit 125, not as a line a card can type"},
		{"marker after RUN, the card's process", afterRun, true, false, "already the card's, and still is"},
		{"marker from go build, which the card does not run", beforeRun, false, true, "the toolchain's own output is the bench's voice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptToolchainRed(tc.out, exitErr(t, 1), tc.cardRuns); got != tc.want {
				t.Errorf("acceptToolchainRed = %v, want %v: %s", got, tc.want, tc.why)
			}
		})
	}

	// The wall's own exits still speak, whoever ran.
	for _, code := range []int{125, 126, 127} {
		if !acceptToolchainRed("", exitErr(t, code), true) {
			t.Errorf("exit %d is the wall answering and must stay the bench's", code)
		}
	}
	// A command that could not be started at all is the bench's too.
	if !acceptToolchainRed("", errors.New("fork/exec: no such file or directory"), true) {
		t.Error("a command that never started is the bench's")
	}
}

// The end-to-end receipt: a card whose TestMain prints the wall's words and whose test
// then fails is REJECTed, not abstained.
func TestAcceptAWallMarkerFromTestMainIsStillTheCardsRed(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", strings.Replace(baseTest, "import \"testing\"", "import (\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)", 1)+
		"\nfunc TestMain(m *testing.M) {\n\tfmt.Println(\"SANDBOX DENIED reason=fake: Operation not permitted\")\n\tos.Exit(m.Run())\n}\n"+
		"\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 1 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "a TestMain that talks like the wall before any test runs")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=red-at-head ")
}

// #1807 (PROBE-B), end to end: the card adds a new symbol and a test that only CALLS it.
// Reverting the fix deletes the symbol, the package stops compiling, and the gate used to
// read `[build failed]` as the kill its negative control was looking for -- so a test that
// asserts nothing reached `ACCEPT OK`. It is a rejection, and the token is the honest one:
// the test is vacuous.
//
// The cost of this rule, said out loud: a card that adds a NEW symbol with a genuinely
// asserting test is rejected the same way, because `mutate`'s revert cannot tell the two
// apart from the outside. That shape wants `mutation-kill`'s seed form (T13) or a spec
// answer on #1637; it is not something this gate can decide by looking.
func TestAcceptRejectsACallOnlyTestCoupledByCompilation(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix+"\n// Mul is new in this change, and it is wrong.\nfunc Mul(a, b int) int { return 0 }\n")
	acceptWrite(t, l.job, "sign/sign_test.go", baseTest+"\nfunc TestSignZero(t *testing.T) {\n\t_ = Mul(2, 3)\n}\n")
	l.commit(t, "a call-only test coupled to the fix by compilation")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=vacuous-test ")
}

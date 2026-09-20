package pulse

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
)

// exitErr is a real *exec.ExitError with the given code, the shape #1806's probe used:
// the marker text with an ordinary exit 1 under it.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit "+strconv.Itoa(code))
	} else {
		cmd = exec.Command("sh", "-c", "exit "+strconv.Itoa(code))
	}
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("wanted a real *exec.ExitError for exit %d, got %v", code, err)
	}
	return ee
}

// #1806. acceptToolchainRed scored a wall marker printed BEFORE the first `=== RUN` as
// the bench's fault. A card's TestMain or package init runs before any `=== RUN`, so a
// card could print `Operation not permitted`, fail with exit 1, and be scored ABSTAIN
// toolchain instead of REJECT -- dodging the track-record fail and staling the bench.
func TestAcceptAWallMarkerFromTheCardsOwnProcessIsNotTheBenchs(t *testing.T) {
	const beforeRun = "Operation not permitted\nFAIL\tgithub.com/x/y\t0.012s\n"
	const testMainOnly = "Operation not permitted\n"
	const sandboxDenied = "SANDBOX DENIED path=/etc/shadow\nFAIL\tgithub.com/x/y\t0.012s\n"
	const afterRun = "=== RUN   TestX\n    x_test.go:9: Operation not permitted\n--- FAIL: TestX\nFAIL\tgithub.com/x/y\t0.012s\n"

	for _, tc := range []struct {
		name     string
		out      string
		cardRuns bool
		want     bool
		why      string
	}{
		{"marker before RUN, the card's process", beforeRun, true, false, "a card's TestMain can print anything; the bench speaks through the exit code"},
		{"TestMain prints the marker and exits, no RUN, no FAIL", testMainOnly, true, false, "os.Exit(1) from TestMain produces no === RUN and no # pkg; that is still the card"},
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

	for _, code := range []int{125, 126, 127} {
		if !acceptToolchainRed("", exitErr(t, code), true) {
			t.Errorf("exit %d is the wall answering and must stay the bench's", code)
		}
	}
	if !acceptToolchainRed("", errors.New("fork/exec: no such file or directory"), true) {
		t.Error("a command that never started is the bench's")
	}
}

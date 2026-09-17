//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestNativeDeadlineAndSignalsEndAStuckHarness (issue #1129). A harness that never
// answers -- here the wall's stand-in runs the fake harness, which sleeps far past the
// wall -- must not outlive the run. `--deadline`, SIGTERM and SIGALRM each kill the
// harness process group, write the abstain with its reason, and exit 1. The run the issue
// names did none of those, lived 2h28m past a 360s wall, and stalled the adoption pass
// through four dev moves.
//
// The child is the real nova-swarm binary, so the signal is delivered to the process the
// adoption pass would signal, not to the test's own process.
func TestNativeDeadlineAndSignalsEndAStuckHarness(t *testing.T) {
	tool, _ := builtBinaries(t)
	harness := nativeHarness(t)
	wall := nativeSandbox(t)

	const (
		stuckCard = "FAKE-SLEEP 20\n" // sleeps far past every wall below
		bound     = 6 * time.Second   // a run ended by its wall returns well inside this
	)
	for _, tc := range []struct {
		name    string
		wallArg string
		signal  syscall.Signal
		reason  string
	}{
		{name: "deadline", wallArg: "1s", reason: "deadline"},
		{name: "sigterm", wallArg: "30s", signal: syscall.SIGTERM, reason: "terminated"},
		{name: "sigalrm", wallArg: "30s", signal: syscall.SIGALRM, reason: "deadline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := filepath.Join(root, "card.md")
			if err := os.WriteFile(card, []byte(stuckCard), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{
				"native", "--harness", harness, "--model", "fake/fake-model",
				"--label", tc.name, "--card", card, "--slot", slot, "--root", root,
				"--sandbox", wall, "--deadline", tc.wallArg,
			}
			cmd := exec.Command(tool, args...)
			var out, errb strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errb
			start := time.Now()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if tc.signal != 0 {
				// The signal is sent only once the wall has opened and the harness is
				// running, so the process is in the state the adoption pass's alarm
				// finds it in -- not a signal racing the launch.
				awaitFileContains(t, filepath.Join(slot, "jobs", tc.name, "harness-output.log"), "SANDBOX OK", bound)
				if err := cmd.Process.Signal(tc.signal); err != nil {
					t.Fatal(err)
				}
			}
			waitErr := cmd.Wait()
			elapsed := time.Since(start)
			if elapsed > bound {
				t.Fatalf("the run outlived its stop: took %v, want under %v\nstdout:\n%s\nstderr:\n%s",
					elapsed, bound, out.String(), errb.String())
			}
			code := 0
			if waitErr != nil {
				ee, ok := waitErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("waiting for the run: %v", waitErr)
				}
				code = ee.ExitCode()
			}
			if code != 1 {
				t.Fatalf("a run ended by its wall exits 1, got %d\nstdout:\n%s\nstderr:\n%s",
					code, out.String(), errb.String())
			}
			if want := "reason=" + tc.reason; !strings.Contains(out.String(), want) {
				t.Fatalf("the NATIVE OK line does not name the stop (%s):\n%s", want, out.String())
			}
			raw, err := os.ReadFile(filepath.Join(slot, "jobs", tc.name, "RESULT.md"))
			if err != nil {
				t.Fatalf("the abstain was not written: %v", err)
			}
			if want := "ABSTAIN reason=" + tc.reason; !strings.Contains(string(raw), want) {
				t.Fatalf("the abstain does not name its reason (%s):\n%s", want, raw)
			}
		})
	}
}

// awaitFileContains waits, bounded, for a file to hold a string.
func awaitFileContains(t *testing.T, path, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never held %q within %v", path, want, within)
}

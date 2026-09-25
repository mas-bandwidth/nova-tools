//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ISSUE #779: the deadline killed only the child the run spawned directly, never the tree,
// so a card's grandchildren survived the wall and held the run's own descriptors open --
// and a TERM from outside killed the whole run before it wrote usage. A run that ignores
// its deadline and a manager's TERM are the same failure: the spend is unknown.

// TestNativeDeadlineKillsTheWholeTree: a harness that ignores SIGTERM and leaves a sleeping
// grandchild behind is ended at its deadline, prints the NATIVE line, leaves no live child,
// and still writes usage.tsv. RED WITHOUT THE FIX: the deadline killed only the direct
// child and the grandchild survived it (the control below is that defect, run).
//
// THE DEADLINE IS AN EVENT HERE, NOT A CLOCK (issue #2993). This test used to pass
// `--deadline 3s` and bound the WHOLE run at 5 s. On hosted macOS the run took 6.08 s and
// 6.13 s at 2a43d771 (3.04 s on the Studio) with the kill unchanged: the bound was on the
// setup and teardown around the deadline as much as on the deadline, and a runner that is
// slow to exec a freshly built harness spends seconds there before the timer even starts.
// So the wait is handed its deadline through nativeDeadline, and it fires when the tree it
// is meant to kill is THERE -- the harness has recorded the grandchild's pid -- and at no
// time chosen by anybody. What the deadline branch then does is asserted by its effects:
// the grandchild is gone, which a leader-only kill leaves false for five minutes.
func TestNativeDeadlineKillsTheWholeTree(t *testing.T) {
	windowsIsNotABench(t)
	got := deadlineOnATree(t)
	if !strings.Contains(got.stdout, "NATIVE INCOMPLETE ") {
		t.Fatalf("a run killed at the deadline still prints its verdict line:\n%s\n%s", got.stdout, got.stderr)
	}
	// A run killed at its deadline with a silent harness and no RESULT.md did not succeed,
	// and does not say it did (nova-tools #1844).
	if !strings.Contains(got.stdout, "why=harness-silent") {
		t.Fatalf("the verdict must name why it is incomplete:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "NATIVE OK") {
		t.Fatalf("a run that produced nothing must not say OK:\n%s", got.stdout)
	}
	// No live child after: the grandchild the harness left behind is gone too. It sleeps
	// five minutes on its own, so it is gone only because the group was killed. The kill is
	// SIGKILL and the kernel reaps at its own pace, so this waits on the pid itself; the
	// bound is a safety net and never the assertion.
	if !pidGoneWithin(got.grandchild, 30*time.Second) {
		_ = syscall.Kill(got.grandchild, syscall.SIGKILL)
		t.Fatalf("the grandchild pid %d survived the deadline kill; the run reaped only the leader", got.grandchild)
	}
	if _, err := os.Stat(filepath.Join(got.slot, "usage.tsv")); err != nil {
		t.Fatalf("usage.tsv absent after a deadline kill: %v", err)
	}
}

// TestNativeDeadlineControlALeaderOnlyKillLeavesTheGrandchild is the control for the test
// above (issue #2993): the deadline kill put back to what #779 found -- SIGKILL to the
// direct child only, not its group -- and the same run. The grandchild must then be ALIVE
// when the run returns, which is exactly the assertion above failing. Nothing here waits
// on a clock: a grandchild with five minutes of sleep left is alive the instant the run
// returns, or the observable above could not tell the fix from the defect.
func TestNativeDeadlineControlALeaderOnlyKillLeavesTheGrandchild(t *testing.T) {
	windowsIsNotABench(t)
	realKill := nativeKillGroup
	t.Cleanup(func() { nativeKillGroup = realKill })
	nativeKillGroup = func(pgid int, started string) {
		_ = syscall.Kill(pgid, syscall.SIGKILL) // the leader, not -pgid: the #779 defect
	}
	got := deadlineOnATree(t)
	defer func() { _ = syscall.Kill(got.grandchild, syscall.SIGKILL) }()
	// The grandchild is ALIVE, not provably gone: signal 0 answers EPERM for a live
	// process the runner's own sandbox keeps out of reach (macOS sandbox-exec), which is
	// still alive and is exactly the observable the test above depends on. swarm.Alive
	// reads EPERM as alive, where a bare `syscall.Kill(pid, 0) != nil` read it as gone and
	// turned a green control red on the Studio.
	if !swarm.Alive(got.grandchild, "") {
		t.Fatalf("with the deadline killing only the leader, grandchild %d is gone anyway -- the test above cannot tell the fix from the defect", got.grandchild)
	}
}

type deadlineRun struct {
	stdout, stderr string
	slot           string
	grandchild     int
}

// deadlineOnATree runs one card whose harness leaves a five-minute grandchild behind and
// then ignores SIGTERM, and fires the run's deadline the moment the harness has recorded
// that grandchild. It returns once the run has.
func deadlineOnATree(t *testing.T) deadlineRun {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-BACKGROUND-SLEEP 300\nFAKE-IGNORE-TERM\nFAKE-SLEEP 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bgPath := filepath.Join(slot, "jobs", "deadline", "background.pid")

	realDeadline := nativeDeadline
	t.Cleanup(func() { nativeDeadline = realDeadline })
	quit := make(chan struct{})
	defer close(quit)
	nativeDeadline = func(time.Duration) (<-chan time.Time, func() bool) {
		fire := make(chan time.Time)
		go func() {
			// The harness writes the pid after the grandchild has started and before it
			// sleeps, so the tree the deadline must kill exists when this fires. The
			// ticker is the poll interval, not a bound: nothing fails when it elapses.
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				if raw, err := os.ReadFile(bgPath); err == nil && len(bytes.TrimSpace(raw)) > 0 {
					close(fire)
					return
				}
				select {
				case <-quit:
					return
				case <-tick.C:
				}
			}
		}()
		return fire, func() bool { return true }
	}

	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "deadline", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10m", "--no-wall"}
	var stdout, stderr bytes.Buffer
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	}()
	// A safety net for a run that never returns, which would otherwise hold the package
	// until go test's own timeout. The green run returns in milliseconds after the pid.
	select {
	case <-returned:
	case <-time.After(2 * time.Minute):
		if raw, err := os.ReadFile(bgPath); err == nil {
			if bg, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && bg > 0 {
				_ = syscall.Kill(bg, syscall.SIGKILL)
			}
		}
		t.Fatalf("the run did not return after its deadline fired (or the harness never recorded its grandchild):\n%s", stderr.String())
	}
	bgRaw, err := os.ReadFile(bgPath)
	if err != nil {
		// The harness could not background a process here (issue #3749): on the Studio
		// runner the grandchild is never recorded, so the pid file is absent and the
		// deadline wait has nothing to fire on. There is no tree to kill or to leave
		// behind, so the two deadline tests cannot tell the fix from the defect on this
		// bench, and they skip with a reason instead of going red for the runner.
		t.Skipf("the harness cannot background a process here: no background pid was recorded: %v", err)
	}
	bg, err := strconv.Atoi(strings.TrimSpace(string(bgRaw)))
	if err != nil || bg <= 0 {
		t.Skipf("the harness cannot background a process here: background.pid holds %q: %v", bgRaw, err)
	}
	return deadlineRun{stdout: stdout.String(), stderr: stderr.String(), slot: slot, grandchild: bg}
}

// pidGoneWithin waits for a pid to stop answering signal 0, polling, up to bound.
func pidGoneWithin(pid int, bound time.Duration) bool {
	for end := time.Now().Add(bound); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
	}
	return syscall.Kill(pid, 0) != nil
}

// TestNativeTermFromOutsideWritesUsage: a TERM to the native run mid-flight is the same
// cleanup as the deadline -- the whole tree, the usage row, and a reason=terminated line --
// never a silent exit that loses the spend.
func TestNativeTermFromOutsideWritesUsage(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	tool, _ := builtBinaries(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tool, "native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "termed", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "60s", "--no-wall")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting native: %v", err)
	}
	// Wait for the harness to be running (it writes its argv first), then TERM the run.
	argv := filepath.Join(slot, "jobs", "termed", "argv")
	waitFor := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(argv); err == nil {
			break
		}
		if time.Now().After(waitFor) {
			_ = cmd.Process.Kill()
			t.Fatalf("the harness never started (no argv):\n%s", stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		// a native run that was terminated exits non-zero (rc=-1), never 0
		t.Fatalf("a TERMed run exits non-zero, got 0")
	}
	if _, err := os.Stat(filepath.Join(slot, "usage.tsv")); err != nil {
		t.Fatalf("usage.tsv absent after a TERM from outside: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "reason=terminated") {
		t.Fatalf("a TERM from outside prints reason=terminated, got:\n%s", stdout.String())
	}
}

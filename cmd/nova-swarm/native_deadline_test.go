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
)

// ISSUE #779: the deadline killed only the child the run spawned directly, never the tree,
// so a card's grandchildren survived the wall and held the run's own descriptors open --
// and a TERM from outside killed the whole run before it wrote usage. A run that ignores
// its deadline and a manager's TERM are the same failure: the spend is unknown.

// TestNativeDeadlineKillsTheWholeTree: a harness that ignores SIGTERM and leaves a sleeping
// grandchild behind, with --deadline 3s, must print the NATIVE line within 5s, leave no
// live child, and still write usage.tsv. RED WITHOUT THE FIX: the run's deadline killed
// only the direct child, the grandchild survived and the run did not return until it went.
func TestNativeDeadlineKillsTheWholeTree(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-BACKGROUND-SLEEP 30\nFAKE-IGNORE-TERM\nFAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "deadline", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "3s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	elapsed := time.Since(start)
	if elapsed > 5*time.Second { // wall-ok: the bound is the assertion -- with the fix the deadline cuts the tree at 3s, and without it the run blocks on the surviving grandchild; a generous bound would be blind to the regression
		t.Fatalf("the deadline cut the run at 3s, but it took %v:\n%s", elapsed, stderr.String())
	}
	// The verdict line is still printed -- that is what this assertion has always been
	// about -- but a run killed at its deadline with a silent harness and no RESULT.md
	// did not succeed, and no longer says it did (nova-tools #1844).
	if !strings.Contains(stdout.String(), "NATIVE INCOMPLETE ") {
		t.Fatalf("a run killed at the deadline still prints its verdict line:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "why=harness-silent") {
		t.Fatalf("the verdict must name why it is incomplete:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("a run that produced nothing must not say OK:\n%s", stdout.String())
	}
	// no live child after: the grandchild the harness left behind is gone too.
	bgRaw, err := os.ReadFile(filepath.Join(slot, "jobs", "deadline", "background.pid"))
	if err != nil {
		t.Fatalf("the harness recorded no background pid: %v", err)
	}
	bg, err := strconv.Atoi(strings.TrimSpace(string(bgRaw)))
	if err != nil || bg <= 0 {
		t.Fatalf("background pid %q: %v", bgRaw, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(bg, 0) != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(bg, 0); err == nil {
		t.Fatalf("the grandchild pid %d survived the deadline kill; the run reaped only the leader", bg)
	}
	if _, err := os.Stat(filepath.Join(slot, "usage.tsv")); err != nil {
		t.Fatalf("usage.tsv absent after a deadline kill: %v", err)
	}
	_ = rc
}

// TestNativeTermFromOutsideWritesUsage: a TERM to the native run mid-flight is the same
// cleanup as the deadline -- the whole tree, the usage row, and a reason=terminated line --
// never a silent exit that loses the spend.
func TestNativeTermFromOutsideWritesUsage(t *testing.T) {
	windowsIsNotABench(t)
	tool, _ := builtBinaries(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tool, "native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
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

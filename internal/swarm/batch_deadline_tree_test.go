//go:build unix

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ISSUE #640: a deadline abstain leaves the harness's process tree running. The dogfood of
// 2026-09-16 (tool-pulse probe TP1) ended a six-card batch with `BATCH ... abstain=6` and
// then, five to twelve minutes later, still showed under the root two nova-sandbox wrappers,
// five nova-*.test binaries and a go-build cache process -- the card's harness tree, which
// the deadline killed only one process of.
//
// The red test: a card whose harness spawns a sleeping child in a NEW SESSION (`setsid
// sleep 300`, the shape a process-group kill cannot reach) and writes the child's pid under
// the slot dir, then sleeps past the deadline itself. After the BATCH line no process of the
// card is alive, and the card's ABSTAIN line says how many pids the deadline kill reached.
func TestBatchDeadlineKillsTheWholeTree(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid on PATH; this test names a session-leader child by that name")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The harness spawns `setsid sleep 300`, records its pid under the slot, then sleeps
	// past the deadline without publishing: only a kill that walks the whole tree reaches
	// the child, and the child's pid is the proof it was left running or killed.
	runner := runnerDoing(t, dir, "sleeper",
		runnerStep{Op: "spawn-setsid", Path: "{root}/{slot}/sleep.pid"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	// The deadline is fired by the injected clock, never by the machine: the test waits
	// until the runner has spawned its setsid child, then advances the clock so the kill
	// lands on a child that is certainly alive (issue #916's wall-clock law).
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitDeadline()
		waitForFile(t, filepath.Join(root, "1", "sleep.pid"))
		clk.advance(30 * time.Second)
	})
	if code != 1 {
		t.Fatalf("a card killed at the deadline exits 1, got %d:\n%s\n%s", code, out, errs)
	}
	pidRaw, err := os.ReadFile(filepath.Join(root, "1", "sleep.pid"))
	if err != nil {
		t.Fatalf("the harness must have written its child's pid under the slot dir: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
	if err != nil || child < 1 {
		t.Fatalf("sleep.pid wants a pid, got %q", strings.TrimSpace(string(pidRaw)))
	}
	// The child was killed with the runner; the kernel reaps it a moment later. Polling
	// until it is gone is the assertion -- a child the deadline left running stays alive
	// for its whole 300 s and fails the poll.
	gone := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !Alive(child, "") {
			gone = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("after the BATCH line a process of the card is still running (setsid child pid %d): a BATCH line means nothing of the batch is still running", child)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=deadline killed=2 pids log=0") {
		t.Fatalf("the deadline abstain names how many pids it killed (the runner and its child):\n%s", out)
	}
}

//go:build functional

package swarm

import (
	"bytes"
	"testing"
	"time"
)

// Helpers only the functional tier's tests use: they drive a real process, a
// git history or a store on disk (Glenn 2026-09-26, nova-tools#4328).

// runBatchClock drives one Batch under the given manual clock. drive runs while the
// batch is under way; it is where a test advances time or waits for a file the
// runner wrote. The process is a real executable and its exit is real: only the
// clock the kill logic reads is injected.
func runBatchClock(in BatchInput, clk *manualClock, drive func()) (int, string, string) {
	in.clock = clk
	in.polled = func() { clk.polled <- struct{}{} }
	var out, errb bytes.Buffer
	in.Stdout = &out
	in.Stderr = &errb
	done := make(chan int, 1)
	go func() { done <- Batch(in); close(clk.gone) }()
	drive()
	code := <-done
	return code, out.String(), errb.String()
}

// waitForFile waits, against a thirty-second real bound and never an assertion,
// until path exists. The runner is a real process and this is a readiness wait on
// its work, not a claim about the machine.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if fileExists(path) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %s: timed out", path)
}

// waitForLog waits until path holds want non-header output lines.
func waitForLog(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if logOutputLines(path) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %s to hold %d log lines: timed out", path, want)
}

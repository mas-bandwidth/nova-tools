//go:build functional

package swarm

import (
	"os/exec"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestOSProcessTreeCPUAccrual verifies that newProcSnapshot and TreeCPU accurately read
// the host OS process table and accumulate real CPU time (via kern.proc.all and
// proc_pidinfo on Darwin, or /proc on Linux) against a live burning subprocess.
func TestOSProcessTreeCPUAccrual(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	windowsIsNotABench(t)
	bin := builtFakeRunner(t)

	// Start a burner subprocess with an independent 30-second watchdog deadline.
	// The burner runs until the parent observes the CPU milestone and terminates it,
	// eliminating any race against fixed sleep durations.
	cmd := exec.Command(bin, "--spin-ms", "30000")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting burner: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	pid := cmd.Process.Pid
	const floor = 50 * time.Millisecond
	deadline := time.Now().Add(10 * time.Second) // wall-ok: real OS sampler milestone with watchdog

	var initialCPU uint64
	var sawInitial bool
	for time.Now().Before(deadline) {
		snap := newProcSnapshot()
		if cpu, ok := snap.TreeCPU(pid); ok {
			initialCPU = cpu
			sawInitial = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawInitial {
		t.Fatalf("process %d was not visible in procSnapshot", pid)
	}

	var accrued bool
	for time.Now().Before(deadline) {
		snap := newProcSnapshot()
		if cpu, ok := snap.TreeCPU(pid); ok {
			if cpu >= initialCPU+uint64(floor) {
				accrued = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !accrued {
		t.Fatalf("procSnapshot did not observe %v CPU accrual for pid %d before deadline", floor, pid)
	}
}

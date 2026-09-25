package swarm

import (
	"os/exec"
	"testing"
	"time"
)

// TestOSProcessTreeCPUAccrual verifies that newProcSnapshot and TreeCPU accurately read
// the host OS process table and accumulate real CPU time (via kern.proc.all and
// proc_pidinfo on Darwin, or /proc on Linux) against a live burning subprocess.
func TestOSProcessTreeCPUAccrual(t *testing.T) {
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

// TestOSProcessTreeCPUNonExistent verifies that TreeCPU returns ok=false for an
// unknown or dead PID rather than pretending zero or inventing activity.
func TestOSProcessTreeCPUNonExistent(t *testing.T) {
	windowsIsNotABench(t)
	snap := newProcSnapshot()
	// Negative PID, zero PID, and a PID exceedingly unlikely to exist:
	for _, pid := range []int{-1, 0, 999_999} {
		if _, ok := snap.TreeCPU(pid); ok {
			t.Fatalf("TreeCPU(%d) = true, want false for non-existent pid", pid)
		}
	}
}

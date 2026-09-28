package swarm

import (
	"testing"
)

// TestOSProcessTreeCPUNonExistent verifies that TreeCPU returns ok=false for an
// unknown or dead PID rather than pretending zero or inventing activity.
func TestOSProcessTreeCPUNonExistent(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	snap := newProcSnapshot()
	// Negative PID, zero PID, and a PID exceedingly unlikely to exist:
	for _, pid := range []int{-1, 0, 999_999} {
		if _, ok := snap.TreeCPU(pid); ok {
			t.Fatalf("TreeCPU(%d) = true, want false for non-existent pid", pid)
		}
	}
}

package swarm

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOSProcessTreeCPUNonExistent verifies that TreeCPU returns ok=false for an
// unknown or dead PID rather than pretending zero or inventing activity.
func TestOSProcessTreeCPUNonExistent(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	snap := newProcSnapshot()
	// Negative PID, zero PID, and a PID exceedingly unlikely to exist:
	for _, pid := range []int{-1, 0, 999_999} {
		_, ok := snap.TreeCPU(pid)
		require.False(t, ok, "TreeCPU(%d) = true, want false for non-existent pid", pid)
	}
}

// TestTreeCPUReadsOnlyAKnownSnapshotsLiveTree pins the tree TreeCPU walks: from a
// live pid, through the live processes it fathered, and nothing else. The
// snapshots are built by hand around the test's own process, whose CPU time any
// platform can read.
func TestTreeCPUReadsOnlyAKnownSnapshotsLiveTree(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	self := os.Getpid()
	const gone = 1 << 30 // a pid no table holds live
	for _, c := range []struct {
		name string
		snap *procSnapshot
		pid  int
		want bool
	}{
		{"no snapshot", nil, self, false},
		{"a table the platform could not read", &procSnapshot{live: map[int]bool{self: true}}, self, false},
		{"a live pid", &procSnapshot{live: map[int]bool{self: true}, known: true}, self, true},
		{"a pid that is not live", &procSnapshot{live: map[int]bool{}, known: true}, self, false},
		{"no pid", &procSnapshot{live: map[int]bool{self: true}, known: true}, 0, false},
		{"a live child of a live pid", &procSnapshot{live: map[int]bool{gone: true, self: true}, children: map[int][]int{gone: {self}}, known: true}, gone, true},
		{"a live child of a pid that is not live", &procSnapshot{live: map[int]bool{self: true}, children: map[int][]int{gone: {self}}, known: true}, gone, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, ok := c.snap.TreeCPU(c.pid)
			assert.Equal(t, c.want, ok, "%s: TreeCPU(%d) ok = %v, want %v", c.name, c.pid, ok, c.want)
		})
	}
}

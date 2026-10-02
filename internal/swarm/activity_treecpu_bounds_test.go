package swarm

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TreeCPU returns ok=false when pid <= 0 even if the snapshot table holds non-positive keys.
func TestTreeCPURefusesNonPositivePIDs(t *testing.T) {
	t.Parallel()

	self := os.Getpid()
	snap := &procSnapshot{
		live:     map[int]bool{0: true, -1: true, self: true},
		children: map[int][]int{0: {-1}},
		known:    true,
	}

	for _, pid := range []int{0, -1, -42} {
		cpu, ok := snap.TreeCPU(pid)
		assert.False(t, ok, "TreeCPU(%d) ok should be false", pid)
		assert.Zero(t, cpu, "TreeCPU(%d) cpu should be 0", pid)
	}
}

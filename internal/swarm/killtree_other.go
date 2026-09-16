//go:build !unix

package swarm

import "os"

// killTree on a platform with no process table this repo can read ends the runner itself
// and counts it alone: the tree was never enumerable here, and a runner killed is the
// deadline the machine can keep.
func killTree(pid int) int {
	if pid <= 0 {
		return 0
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
		return 1
	}
	return 0
}

//go:build !darwin && !linux

package swarm

// The platform where the process table has no route this repo can take without a dependency
// it does not have. The honest shape is a degraded one rather than a pretended one: the
// snapshot knows nothing, TreeCPU says so on its second return, and the idle monitor watches
// the card's log alone -- exactly what it watched before issue #593. A card here is killed
// for a silent log as it always was, and the line it prints says which file it watched.

func newProcSnapshot() *procSnapshot { return &procSnapshot{} }

func cpuOf(pid int) (uint64, bool) { return 0, false }

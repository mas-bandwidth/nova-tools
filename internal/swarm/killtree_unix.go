//go:build unix

package swarm

import (
	"os"
	"syscall"
)

// killTree ends a card's whole process tree: the runner and every descendant of it in the
// process table, and returns how many pids it killed. A deadline that killed only the runner
// left the harness's tree running -- the sandbox wrapper, its go test binaries, a go-build
// cache process -- for minutes after the BATCH line (issue #640). The tree is read ONCE,
// before any kill, so a descendant that a parent's death orphans (reparented to init) is
// still reached; a child that setid'd itself into a new session is reached because the walk
// follows parents, never groups. Nothing here matches a command line and nothing runs `ps`.
func killTree(pid int) int {
	if pid <= 0 {
		return 0
	}
	snap := newProcSnapshot()
	tree := []int{}
	if snap.known {
		seen := map[int]bool{}
		stack := []int{pid}
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[p] {
				continue
			}
			seen[p] = true
			if !snap.live[p] {
				continue
			}
			tree = append(tree, p)
			stack = append(stack, snap.children[p]...)
		}
	} else {
		tree = []int{pid}
	}
	killed := 0
	for _, p := range tree {
		if p == os.Getpid() {
			continue
		}
		if err := syscall.Kill(p, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
}

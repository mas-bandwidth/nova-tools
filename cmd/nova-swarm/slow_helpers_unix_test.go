//go:build unix && (slow || functional)

// The unix helpers of the slow and functional tiers: every caller sits in a file behind the
// `slow` or `functional` tag (slow_unix_test.go, slow_test.go and the *_functional_test.go
// files), so these sit behind the same tags. Left in an untagged file they are dead code to
// the unit tier, and staticcheck, which reads the tree untagged, reports each as U1000.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// reapLeftoverSupervise ends every supervisor and harness group this bench still has on
// disk, then waits. t.Cleanup on newBench calls it so a subtest that forked a supervise
// and returned without noting the pid cannot leave a process that ignores TERM (#1598).
func reapLeftoverSupervise(b *bench) {
	seen := map[int]bool{}
	reap := func(pid int) {
		if pid <= 1 || seen[pid] {
			return
		}
		seen[pid] = true
		reapLeftoverPID(pid)
	}
	if b.dir != "" {
		_ = filepath.WalkDir(b.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "supervisor.pid" {
				return err
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr == nil {
				reap(pid)
			}
			return nil
		})
	}
}

// shQuote single-quotes s for safe inclusion in a /bin/sh script.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// readPID reads a pid from a file, returning 0 on error or parse failure.
func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// alive reports whether pid exists and is not a zombie. A bare kill(pid,0) succeeds
// for a zombie, so the process state is read instead.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "state=").Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	if len(state) == 0 {
		return false
	}
	// Z = zombie: the process has exited and has not been reaped; it is not alive.
	return state[0] != 'Z'
}

// signalGroup sends KILL to the process group of a pid this test started, ignoring
// "no such process".
func signalGroup(pid int) {
	if pid <= 0 {
		return
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

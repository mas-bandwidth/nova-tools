//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
	if b.pool == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(b.pool, "slots"))
	if err != nil {
		return
	}
	for _, e := range entries {
		var sf swarm.SlotFile
		if swarm.ReadJSON(filepath.Join(b.pool, "slots", e.Name()), &sf) != nil {
			continue
		}
		reap(sf.Pid)
		reap(sf.Pgid)
		reap(sf.JobPgid)
	}
}

func reapLeftoverPID(pid int) {
	if pid <= 1 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGCONT)
	swarm.Reap(pid, "", swarm.TerminateGrace)
}

// leftoverChildPIDs is the wait-style regression: processes whose parent is still this
// test binary. Orphaned supervise (ppid 1) is the other half, caught by the pid the test
// recorded; git maintenance is already not a child by the time you look (#1607).
func leftoverChildPIDs() []int {
	self := os.Getpid()
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || pid == self || ppid != self {
			continue
		}
		// This listing is itself a child of the test binary; counting it is a false leak.
		if fields[2] == "ps" {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

//go:build darwin

package swarm

import (
	"encoding/binary"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The process table on a system whose standard library will not hand it over.
//
// kern.proc.all is an array of kinfo_proc, the call ps makes. Four fields of each record are read here and no other: the pid, the parent pid, the state and the command name.
// The CPU time is NOT in that record -- the kernel leaves p_cpticks, p_uticks and p_pctcpu
// zero on this system, which a probe against a spinning child and a sleeping one shows at
// once -- so the time itself comes from proc_info(PROC_PIDTASKINFO), the call libproc makes,
// asked only about the pids of the tree in hand.

const (
	// darwinStopped is SSTOP, kinfo_proc's p_stat of a process stopped by a signal (a
	// stopped target, as `leaks --atExit` leaves one waiting for an authorization).
	darwinStopped = 4

	// procInfoCallPIDInfo and procPIDTaskInfo are proc_info(2)'s call number and flavor, and
	// procTaskInfoSize is sizeof(struct proc_taskinfo). pti_total_user and pti_total_system
	// are the third and fourth quads of that record, in nanoseconds.
	sysProcInfo         = 336
	procInfoCallPIDInfo = 2
	procPIDTaskInfo     = 4
	procTaskInfoSize    = 96
	ptiTotalUserOff     = 16
	ptiTotalSystemOff   = 24
)

// newProcSnapshot reads the whole table once: every live pid, the pid that fathered it, and
// the state and command name of a stopped one.
func newProcSnapshot() *procSnapshot {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return &procSnapshot{}
	}
	s := &procSnapshot{live: map[int]bool{}, children: map[int][]int{}, stopped: map[int]bool{}, command: map[int]string{}, known: true}
	for i := range procs {
		p := &procs[i]
		pid := int(p.Proc.P_pid)
		ppid := int(p.Eproc.Ppid)
		if pid <= 0 {
			continue
		}
		s.live[pid] = true
		if p.Proc.P_stat == darwinStopped {
			s.stopped[pid] = true
			s.command[pid] = strings.TrimRight(string(p.Proc.P_comm[:]), "\x00")
		}
		if ppid > 0 {
			s.children[ppid] = append(s.children[ppid], pid)
		}
	}
	return s
}

// cpuOf is one process's user plus system time in nanoseconds, or false when the process is
// gone or is not this user's to ask about.
func cpuOf(pid int) (uint64, bool) {
	var buf [procTaskInfoSize]byte
	r1, _, errno := syscall.Syscall6(sysProcInfo, procInfoCallPIDInfo, uintptr(pid),
		procPIDTaskInfo, 0, uintptr(unsafe.Pointer(&buf[0])), procTaskInfoSize)
	if errno != 0 || r1 != procTaskInfoSize {
		return 0, false
	}
	return binary.LittleEndian.Uint64(buf[ptiTotalUserOff:ptiTotalUserOff+8]) +
		binary.LittleEndian.Uint64(buf[ptiTotalSystemOff:ptiTotalSystemOff+8]), true
}

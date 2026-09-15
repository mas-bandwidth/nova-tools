//go:build darwin

package swarm

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

// The process table on a system whose standard library will not hand it over.
//
// kern.proc.all is an array of kinfo_proc, the same call internal/wake reads for its procs=
// count. Two fields of each record are read here and no other: the pid, and the parent pid.
// The CPU time is NOT in that record -- the kernel leaves p_cpticks, p_uticks and p_pctcpu
// zero on this system, which a probe against a spinning child and a sleeping one shows at
// once -- so the time itself comes from proc_info(PROC_PIDTASKINFO), the call libproc makes,
// asked only about the pids of the tree in hand.

const (
	// kinfoProcSize is sizeof(kinfo_proc) on 64-bit Darwin, and the two offsets are p_pid in
	// struct extern_proc and e_ppid in struct eproc. They are in no standard-library type, so
	// they are NAMED CONSTANTS here -- a test cannot be written from words that leave them
	// unstated.
	kinfoProcSize = 648
	kinfoPIDOff   = 40
	kinfoPPIDOff  = 560

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

// newProcSnapshot reads the whole table once: every live pid and the pid that fathered it.
func newProcSnapshot() *procSnapshot {
	raw, ok := kernProcAll()
	if !ok {
		return &procSnapshot{}
	}
	s := &procSnapshot{live: map[int]bool{}, children: map[int][]int{}, known: true}
	for off := 0; off+kinfoProcSize <= len(raw); off += kinfoProcSize {
		rec := raw[off : off+kinfoProcSize]
		pid := int(int32(binary.LittleEndian.Uint32(rec[kinfoPIDOff : kinfoPIDOff+4])))
		ppid := int(int32(binary.LittleEndian.Uint32(rec[kinfoPPIDOff : kinfoPPIDOff+4])))
		if pid <= 0 {
			continue
		}
		s.live[pid] = true
		if ppid > 0 {
			s.children[ppid] = append(s.children[ppid], pid)
		}
	}
	return s
}

// kernProcAll asks sysctl for the table twice, as the call requires: once with a nil buffer
// for the length, once for the bytes.
func kernProcAll() ([]byte, bool) {
	mib := [3]int32{1, 14, 0} // CTL_KERN, KERN_PROC, KERN_PROC_ALL
	var n uintptr
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3, 0, uintptr(unsafe.Pointer(&n)), 0, 0)
	if errno != 0 || n == 0 {
		return nil, false
	}
	buf := make([]byte, n)
	_, _, errno = syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0, 0)
	if errno != 0 {
		return nil, false
	}
	if n < uintptr(len(buf)) {
		buf = buf[:n]
	}
	return buf, true
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

//go:build linux

package swarm

import (
	"os"
	"strconv"
	"strings"
)

// The process table here is /proc, and the two fields read are the kernel's own: field 4 of
// /proc/<pid>/stat is the parent pid, fields 14 and 15 are the process's user and system
// time in clock ticks. The comm field is in parentheses and may hold spaces and parentheses
// of its own, so every split starts after the LAST ")" -- reading past the name at all,
// never matching a command line (proc_linux.go reads the same file the same way).

// newProcSnapshot reads the whole table once: every live pid and the pid that fathered it.
func newProcSnapshot() *procSnapshot {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return &procSnapshot{}
	}
	s := &procSnapshot{live: map[int]bool{}, children: map[int][]int{}, known: true}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		s.live[pid] = true
		if fields, ok := statFields(pid); ok && len(fields) >= 2 {
			// fields[0] is state, which is field 3; field 4 -- the parent pid -- is fields[1].
			if ppid, err := strconv.Atoi(fields[1]); err == nil && ppid > 0 {
				s.children[ppid] = append(s.children[ppid], pid)
			}
		}
	}
	return s
}

// cpuOf is one process's user plus system time in clock ticks, or false when the process is
// gone.
func cpuOf(pid int) (uint64, bool) {
	fields, ok := statFields(pid)
	// fields[0] is field 3, so fields[11] and fields[12] are fields 14 and 15: utime, stime.
	if !ok || len(fields) < 13 {
		return 0, false
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return utime + stime, true
}

// statFields is /proc/<pid>/stat from after the comm field on, split on spaces.
func statFields(pid int) ([]string, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, false
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return nil, false
	}
	return strings.Fields(string(raw)[i+1:]), true
}

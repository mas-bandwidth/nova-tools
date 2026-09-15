//go:build linux

package swarm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// StartStamp is the kernel's own start stamp for a pid: field 22 of /proc/<pid>/stat, the
// process's start time in clock ticks since boot. It is what tells a live pid from a REUSED
// one -- a slot file whose pid is alive under a different stamp is a different process
// wearing an old number, and rule 17 quarantines it rather than adopting it.
func StartStamp(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "-"
	}
	// The comm field is in parentheses and may hold spaces and parentheses of its own, so
	// the split starts after the LAST ")". Reading the name at all is reading a field the
	// kernel wrote, never matching a command line.
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return "-"
	}
	fields := strings.Fields(string(raw)[i+1:])
	// fields[0] is state, which is field 3; field 22 is therefore fields[19].
	if len(fields) < 20 {
		return "-"
	}
	return fields[19]
}

// ProcTreeActivity sums the aggregate CPU time (in clock ticks) and I/O bytes of pid and
// its live descendants, reading /proc -- the kernel's own tables of pid and counters, never
// a command line. utime+stime are fields 14 and 15 of /proc/<pid>/stat; read_bytes+write_bytes
// come from /proc/<pid>/io. A pid that has already gone is a zero tree, which is the honest
// answer: it cannot move again. ok is always true here; the route exists on Linux.
func ProcTreeActivity(pid int) (cpu, io int64, ok bool) {
	if pid <= 0 {
		return 0, 0, true
	}
	c, i := procActivity(pid)
	for _, child := range childPids(pid) {
		cc, ci := procTreeActivitySum(child)
		cpu += cc
		io += ci
	}
	return c + cpu, i + io, true
}

// procTreeActivitySum returns pid's own CPU/IO plus its whole descendant subtree's, walked in
// one pass so no process is read twice.
func procTreeActivitySum(pid int) (cpu, io int64) {
	c, i := procActivity(pid)
	for _, child := range childPids(pid) {
		cc, ci := procActivity(child)
		c += cc
		i += ci
		gc, gi := procTreeActivitySum(child)
		c += gc
		i += gi
	}
	return c, i
}

// procActivity is one process's own CPU ticks and I/O bytes. A process that cannot be read
// (already reaped) contributes zero and is not alive; its counter can never advance again.
func procActivity(pid int) (cpu, io int64) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return 0, 0
	}
	fields := strings.Fields(string(raw)[i+1:])
	// fields[0] is state (field 3); utime, field 14, is fields[11], and stime, field 15,
	// is fields[12].
	if len(fields) < 13 {
		return 0, 0
	}
	utime, _ := strconv.ParseInt(fields[11], 10, 64)
	stime, _ := strconv.ParseInt(fields[12], 10, 64)
	ioraw, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	if err != nil {
		return utime + stime, 0
	}
	return utime + stime, sumIOCounters(string(ioraw))
}

// childPids lists pid's direct children from /proc/<pid>/task/<pid>/children, the kernel's
// own table. Nothing here matches a process by its command line.
func childPids(pid int) []int {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(raw)) {
		if c, err := strconv.Atoi(f); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// sumIOCounters folds the read_bytes and write_bytes lines of /proc/<pid>/io into one byte
// total: the bytes the process read and wrote, which a silent compute job still advances.
func sumIOCounters(s string) int64 {
	var sum int64
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if k != "read_bytes" && k != "write_bytes" {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		sum += n
	}
	return sum
}

// GroupMembers counts the live processes in a process group other than self, by reading
// /proc -- the kernel's own table of pid and process group, never a command line.
func GroupMembers(pgid, self int) (int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	n := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		i := strings.LastIndex(string(raw), ")")
		if i < 0 {
			continue
		}
		fields := strings.Fields(string(raw)[i+1:])
		// fields[0] is state (field 3), so the process group, field 5, is fields[2].
		if len(fields) > 2 && fields[2] == strconv.Itoa(pgid) {
			n++
		}
	}
	return n, true
}

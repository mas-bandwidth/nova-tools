package sandbox

import (
	"os"
	"path"
	"strconv"
	"strings"
)

// machineRAM is MemTotal from /proc/meminfo in bytes, 0 when it cannot be read.
func machineRAM() int64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return memTotal(string(raw))
}

// memTotal is the MemTotal line of a /proc/meminfo text in bytes (the kernel prints kB),
// 0 when there is none.
func memTotal(meminfo string) int64 {
	for _, line := range strings.Split(meminfo, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil || kb <= 0 {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}

// cgroupRoot is where the unified cgroup hierarchy is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// machineSlice is the tightest memory.max and pids.max of this process's cgroup and its
// ancestors (the member's login slice, user.slice MemoryMax and TasksMax), 0 for either
// where none is set or none can be read.
func machineSlice() (int64, int) {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0, 0
	}
	return sliceLimits(string(raw), func(p string) (string, error) {
		b, err := os.ReadFile(p)
		return string(b), err
	})
}

// sliceLimits is machineSlice over a /proc/self/cgroup text, with read for the files: the
// unified ("0::") path is walked from the process's own cgroup up to the root, and the
// least numeric memory.max and pids.max met on the way are kept ("max" is no limit).
func sliceLimits(selfCgroup string, read func(string) (string, error)) (int64, int) {
	rel := ""
	for _, line := range strings.Split(selfCgroup, "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			rel = p
		}
	}
	if !strings.HasPrefix(rel, "/") {
		return 0, 0
	}
	var mem int64
	tasks := 0
	for dir := path.Clean(rel); ; dir = path.Dir(dir) {
		at := path.Join(cgroupRoot, dir)
		if v, ok := cgroupLimit(read, path.Join(at, "memory.max")); ok && (mem == 0 || v < mem) {
			mem = v
		}
		if v, ok := cgroupLimit(read, path.Join(at, "pids.max")); ok && v <= 1<<31 && (tasks == 0 || int(v) < tasks) {
			tasks = int(v)
		}
		if dir == "/" {
			return mem, tasks
		}
	}
}

// cgroupLimit is one cgroup limit file's positive number; false for "max", a missing
// file or anything else.
func cgroupLimit(read func(string) (string, error), file string) (int64, bool) {
	s, err := read(file)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

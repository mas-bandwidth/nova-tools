package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// sysAttr puts the command in its own process group, so a signal reaches
// everything it started, and watchdog everything left after a SIGKILL;
// Pdeathsig has the kernel SIGKILL the command itself at once.
func sysAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

const (
	isolatedPath = "/sys/devices/system/cpu/isolated"
	cpuSysfsDir  = "/sys/devices/system/cpu"
)

// usableCPUs is the CPUs this process may run on, less those the kernel
// isolates from the scheduler (isolcpus), which are kept for work placed there
// on purpose.
func usableCPUs() ([]int, error) {
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		return nil, fmt.Errorf("sched_getaffinity: %w", err)
	}
	var allowed []int
	for c, n := 0, set.Count(); n > 0; c++ {
		if set.IsSet(c) {
			allowed, n = append(allowed, c), n-1
		}
	}
	return withoutIsolated(allowed, isolatedPath)
}

func parseCPUList(s string) (map[int]bool, error) {
	out := map[int]bool{}
	for _, r := range strings.FieldsFunc(strings.TrimSpace(s), func(c rune) bool { return c == ',' }) {
		lo, hi, isRange := strings.Cut(r, "-")
		a, err := strconv.Atoi(lo)
		b := a
		if isRange && err == nil {
			b, err = strconv.Atoi(hi)
		}
		if err != nil || a < 0 || b < a {
			return nil, fmt.Errorf("not a CPU list")
		}
		for c := a; c <= b; c++ {
			out[c] = true
		}
	}
	return out, nil
}

// withoutIsolated is allowed less the CPUs the file at path lists in the
// kernel's list form (1-15 or 0,4-7); a missing file lists none.
func withoutIsolated(allowed []int, path string) ([]int, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	isolated := strings.TrimSpace(string(b))
	gone, err := parseCPUList(isolated)
	if err != nil {
		return nil, fmt.Errorf("%s holds %q, not a CPU list", path, isolated)
	}
	usable := slices.DeleteFunc(slices.Clone(allowed), func(c int) bool { return gone[c] })
	if len(usable) == 0 {
		return nil, fmt.Errorf("every CPU this process may use (%s) is isolated", cpuList(allowed))
	}
	return usable, nil
}

func smtGroups(usable []int) [][]int { return readSMTGroups(usable, cpuSysfsDir) }

// readSMTGroups groups usable CPUs into physical-core sibling sets using
// <sysfsDir>/cpu<N>/topology/thread_siblings_list.
func readSMTGroups(usable []int, sysfsDir string) [][]int {
	allow, seen := map[int]bool{}, map[int]bool{}
	for _, c := range usable {
		allow[c] = true
	}
	var groups [][]int
	for _, c := range usable {
		if seen[c] {
			continue
		}
		var g []int
		if b, err := os.ReadFile(filepath.Join(sysfsDir, "cpu"+strconv.Itoa(c), "topology", "thread_siblings_list")); err == nil {
			if m, perr := parseCPUList(string(b)); perr == nil {
				for cpu := range m {
					if allow[cpu] && !seen[cpu] {
						g = append(g, cpu)
					}
				}
			}
		}
		if len(g) == 0 {
			g = []int{c}
		}
		slices.Sort(g)
		for _, cpu := range g {
			seen[cpu] = true
		}
		groups = append(groups, g)
	}
	return groups
}

// pin binds this thread to cpus. Linux affinity is per thread and a child
// starts with its parent thread's, so the thread stays locked to this
// goroutine, which goes on to start the command (tie).
func pin(cpus []int) error {
	runtime.LockOSThread()
	var set unix.CPUSet
	for _, c := range cpus {
		set.Set(c)
	}
	if err := unix.SchedSetaffinity(0, &set); err != nil {
		return fmt.Errorf("sched_setaffinity: %w", err)
	}
	return nil
}

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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

const isolatedPath = "/sys/devices/system/cpu/isolated"

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

// withoutIsolated is allowed less the CPUs the file at path lists in the
// kernel's list form (1-15 or 0,4-7); a missing file lists none.
func withoutIsolated(allowed []int, path string) ([]int, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	isolated := strings.TrimSpace(string(b))
	gone := map[int]bool{}
	for _, r := range strings.FieldsFunc(isolated, func(c rune) bool { return c == ',' }) {
		lo, hi, isRange := strings.Cut(r, "-")
		a, err := strconv.Atoi(lo)
		b := a
		if isRange && err == nil {
			b, err = strconv.Atoi(hi)
		}
		if err != nil {
			return nil, fmt.Errorf("%s holds %q, not a CPU list", path, isolated)
		}
		for c := a; c <= b; c++ {
			gone[c] = true
		}
	}
	usable := slices.DeleteFunc(slices.Clone(allowed), func(c int) bool { return gone[c] })
	if len(usable) == 0 {
		return nil, fmt.Errorf("every CPU this process may use (%s) is isolated", cpuList(allowed))
	}
	return usable, nil
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

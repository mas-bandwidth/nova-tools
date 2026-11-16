//go:build linux

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
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func sysAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

const (
	isolatedPath = "/sys/devices/system/cpu/isolated"
	cpuSysfsDir  = "/sys/devices/system/cpu"
)

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

func withoutIsolated(allowed []int, path string) ([]int, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	gone, err := parseCPUList(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s holds %q, not a CPU list", path, strings.TrimSpace(string(b)))
	}
	usable := slices.DeleteFunc(slices.Clone(allowed), func(c int) bool { return gone[c] })
	if len(usable) == 0 {
		return nil, fmt.Errorf("every CPU this process may use (%s) is isolated", cpuList(allowed))
	}
	return usable, nil
}

func smtGroups(usable []int) [][]int {
	return readSMTGroups(usable, cpuSysfsDir)
}

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

func lowerPriority() error {
	nerr := unix.Setpriority(unix.PRIO_PROCESS, 0, 19)
	var param [1]int32
	_, _, errno := unix.Syscall(unix.SYS_SCHED_SETSCHEDULER, 0, uintptr(unix.SCHED_IDLE), uintptr(unsafe.Pointer(&param[0])))
	if errno != 0 && nerr != nil {
		return errno
	}
	return nil
}

func hostMemoryBytes() uint64 {
	total := linuxMemoryBytes("/proc/meminfo", "/proc/self/cgroup", "/sys/fs/cgroup")
	if total > 0 {
		return total
	}
	var si unix.Sysinfo_t
	if unix.Sysinfo(&si) == nil {
		return uint64(si.Totalram) * uint64(si.Unit)
	}
	return 0
}

func cpuBusyPct(sample time.Duration) (int, bool) {
	b1, t1, ok := readProcStat("/proc/stat")
	if !ok {
		return 0, false
	}
	time.Sleep(sample)
	b2, t2, ok := readProcStat("/proc/stat")
	if !ok || t2 <= t1 {
		return 0, false
	}
	return int((b2 - b1) * 100 / (t2 - t1)), true
}

func readProcStat(path string) (busy, total uint64, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	var vals [10]uint64
	for i := 1; i < len(f) && i <= len(vals); i++ {
		v, err := strconv.ParseUint(f[i], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		vals[i-1] = v
		total += v
	}
	idle := vals[3] + vals[4]
	return total - idle, total, total > 0
}

type inotifyWatcher struct {
	fd int
}

func newOSWatcher(dir string) watcher {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return fallbackWatcher{}
	}
	const mask = unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO | unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_ATTRIB
	for _, p := range []string{dir, filepath.Join(dir, "holds"), filepath.Join(dir, "line")} {
		// ignored: fallback timeout bounds wait if a watch fails
		_, _ = unix.InotifyAddWatch(fd, p, mask)
	}
	return &inotifyWatcher{fd: fd}
}

func (w *inotifyWatcher) drain() {
	var buf [4096]byte
	for {
		if _, err := unix.Read(w.fd, buf[:]); err != nil {
			return
		}
	}
}

func (w *inotifyWatcher) wait(_ []int, d time.Duration) {
	if d <= 0 {
		return
	}
	ms := int(min(d, 2*time.Second).Milliseconds())
	if ms <= 0 {
		ms = 1
	}
	pfd := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	// ignored: EINTR or timeout returns to caller which re-checks deadline and state
	_, _ = unix.Poll(pfd, ms)
	w.drain()
}

func (w *inotifyWatcher) close() {
	// ignored: closing inotify descriptor at end of acquire
	_ = unix.Close(w.fd)
}

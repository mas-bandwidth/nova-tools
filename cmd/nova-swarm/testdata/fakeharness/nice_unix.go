//go:build unix

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// ownNice is this process's nice, by getpriority: darwin answers the nice itself, the
// raw Linux system call 20 minus it (internal/yield's threadNice says why).
func ownNice() (int, error) {
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "linux" {
		return 20 - raw, nil
	}
	return raw, nil
}

// ownBehind is this process's reading of the class a --behind-ci launch puts it in:
// darwin's background state (getpriority PRIO_DARWIN_PROCESS: 1 when set), or on
// Linux its cgroup and that cgroup's cpu.idle.
func ownBehind() string {
	if runtime.GOOS == "darwin" {
		v, err := syscall.Getpriority(4, 0) // PRIO_DARWIN_PROCESS
		if err != nil {
			return "darwin_bg=err"
		}
		return "darwin_bg=" + strconv.Itoa(v)
	}
	cg, _ := os.ReadFile("/proc/self/cgroup")
	path := ""
	for _, line := range strings.Split(string(cg), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			path = p
		}
	}
	idle, err := os.ReadFile("/sys/fs/cgroup" + path + "/cpu.idle")
	if err != nil {
		return "cgroup=" + path + ";idle=unreadable" // inside the Linux wall /sys is not a read root
	}
	return "cgroup=" + path + ";idle=" + strings.TrimSpace(string(idle))
}

//go:build darwin

package member

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func realGOOS() string   { return runtime.GOOS }
func realGOARCH() string { return runtime.GOARCH }

func realCPUInfo() string { return "" }

func realHWCap() (uint, uint) { return 0, 0 }

func realSysctl(name string) (int64, bool) {
	if v, err := unix.SysctlUint64(name); err == nil {
		return int64(v), true
	}
	if v, err := unix.SysctlUint32(name); err == nil {
		return int64(v), true
	}
	return 0, false
}

func realStat(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func realProvisioned() bool { return false }

func XDPNote() string { return "" }

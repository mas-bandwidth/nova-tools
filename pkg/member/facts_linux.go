//go:build linux

package member

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

func realGOOS() string  { return runtime.GOOS }
func realGOARCH() string { return runtime.GOARCH }

func realCPUInfo() string {
	b, _ := os.ReadFile("/proc/cpuinfo")
	return string(b)
}

func realHWCap() (uint, uint) {
	return readAuxvHWCAP()
}

func readAuxvHWCAP() (uint, uint) {
	data, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 0, 0
	}
	var hw, hw2 uint
	for i := 0; i+16 <= len(data); i += 16 {
		var k, v uint64
		for j := 0; j < 8; j++ {
			k |= uint64(data[i+j]) << (8 * j)
			v |= uint64(data[i+8+j]) << (8 * j)
		}
		if k == 0 {
			break
		}
		if k == 16 {
			hw = uint(v)
		}
		if k == 26 {
			hw2 = uint(v)
		}
	}
	return hw, hw2
}

func realSysctl(string) (int64, bool) { return 0, false }

func realStat(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func realProvisioned() bool {
	// only if on PATH, run with deadline
	if _, err := exec.LookPath("rocketnet-provision"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := subproc.Context(ctx, "rocketnet-provision", "verify")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return err == nil
}

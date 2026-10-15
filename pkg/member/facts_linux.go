//go:build linux

package member

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"golang.org/x/sys/unix"
)

func realGOOS() string   { return runtime.GOOS }
func realGOARCH() string { return runtime.GOARCH }

func realCPUInfo() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	return string(b)
}

// AT_HWCAP and AT_HWCAP2 are the auxv keys in linux/include/uapi/linux/auxvec.h.
// golang.org/x/sys/unix.Auxv is the accessor (v0.48); /proc/self/auxv is not read.
const (
	atHWCAP  = 16
	atHWCAP2 = 26
)

func realHWCap() (uint, uint) {
	vec, err := unix.Auxv()
	if err != nil {
		return 0, 0
	}
	var hw, hw2 uint
	for _, kv := range vec {
		switch kv[0] {
		case atHWCAP:
			hw = uint(kv[1])
		case atHWCAP2:
			hw2 = uint(kv[1])
		}
	}
	return hw, hw2
}

func realSysctl(string) (int64, bool) { return 0, false }

func realStat(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// provision runs rocketnet-provision verify once: read-only, never the
// provisioning act. Absent, slow or failing is not provisioned.
var (
	provisionOnce sync.Once
	provisionOK   bool
	provisionWhy  string
)

func realProvisioned() bool {
	provisionOnce.Do(checkProvision)
	return provisionOK
}

// XDPNote is why xdp is absent, for the one start line
// `NOTE facts: xdp not provisioned (<why>)`. Empty when verify exited 0.
func XDPNote() string {
	provisionOnce.Do(checkProvision)
	if provisionOK {
		return ""
	}
	return provisionWhy
}

func checkProvision() {
	// ignored: LookPath's error is the presence answer the probe wants, carried as provisionWhy on the member's one start line
	if _, err := exec.LookPath("rocketnet-provision"); err != nil {
		provisionWhy = "not on PATH"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := subproc.Context(ctx, "rocketnet-provision", "verify")
	out, err := cmd.CombinedOutput()
	if err == nil {
		provisionOK = true
		return
	}
	if ctx.Err() != nil {
		provisionWhy = "deadline"
		return
	}
	msg := strings.TrimSpace(string(out))
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if msg == "" {
		msg = err.Error()
	}
	provisionWhy = msg
}

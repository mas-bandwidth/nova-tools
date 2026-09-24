//go:build unix

package merge

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

func configureStepProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killStepProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func extractRusage(ps *os.ProcessState) Rusage {
	if ps == nil {
		return Rusage{}
	}
	raw := ps.SysUsage()
	ru, ok := raw.(*syscall.Rusage)
	if !ok || ru == nil {
		return Rusage{
			UserCPU:   ps.UserTime(),
			SystemCPU: ps.SystemTime(),
		}
	}
	uCPU := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	sCPU := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	maxRSS := int64(ru.Maxrss)
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		maxRSS *= 1024
	}
	return Rusage{
		UserCPU:   uCPU,
		SystemCPU: sCPU,
		MaxRSS:    maxRSS,
	}
}

//go:build !unix

package merge

import (
	"os"
	"os/exec"
)

func configureStepProcess(cmd *exec.Cmd) {}

func killStepProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func extractRusage(ps *os.ProcessState) Rusage {
	if ps == nil {
		return Rusage{}
	}
	return Rusage{
		UserCPU:   ps.UserTime(),
		SystemCPU: ps.SystemTime(),
		MaxRSS:    0,
	}
}

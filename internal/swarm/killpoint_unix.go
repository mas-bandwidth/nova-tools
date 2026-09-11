//go:build unix

package swarm

import (
	"os"
	"syscall"
)

// CheckKillPoint checks if NOVA_SWARM_KILLPOINT matches point, and if so,
// terminates the process immediately using SIGKILL.
func CheckKillPoint(point string) {
	if kp := os.Getenv("NOVA_SWARM_KILLPOINT"); kp != "" && kp == point {
		if point == "between-exit-and-exit-json" || point == "supervisor-after-release" {
			ppid := os.Getppid()
			if ppid > 1 {
				_ = syscall.Kill(ppid, syscall.SIGKILL)
			}
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		os.Exit(137)
	}
}

// CheckPausePoint checks if NOVA_SWARM_PAUSEPOINT matches point, and if so,
// stops the process with SIGSTOP until resumed with SIGCONT.
func CheckPausePoint(point string) {
	if pp := os.Getenv("NOVA_SWARM_PAUSEPOINT"); pp != "" && pp == point {
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
	}
}

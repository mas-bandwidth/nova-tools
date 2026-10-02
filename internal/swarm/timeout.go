package swarm

import (
	"strconv"
)

// TaskkillArgs returns the argument list for Windows taskkill.exe to reap a process tree.
// - When force is false, it returns ["/PID", "<pid>", "/T"] for graceful tree termination.
// - When force is true, it returns ["/PID", "<pid>", "/T", "/F"] for forceful tree termination.
// This function is platform-independent so unit tests on any OS can verify argument construction.
func TaskkillArgs(pid int, force bool) []string {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return args
}

// WindowsKillStrategy specifies the termination mechanism on Windows: taskkill vs direct syscall.
type WindowsKillStrategy int

const (
	// StrategyTaskkill uses taskkill.exe /T (/F) to terminate the full process tree.
	StrategyTaskkill WindowsKillStrategy = iota

	// StrategySyscall uses direct Win32 syscall (TerminateProcess / os.Process.Kill) on the PID.
	StrategySyscall
)

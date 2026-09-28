package yield

import "syscall"

// setNice is setpriority(PRIO_PROCESS, 0, n) on this process.
func setNice(n int) error { return syscall.Setpriority(syscall.PRIO_PROCESS, 0, n) }

// currentNice is getpriority(PRIO_PROCESS, 0): darwin returns the nice value
// as it is.
func currentNice() (int, error) { return syscall.Getpriority(syscall.PRIO_PROCESS, 0) }

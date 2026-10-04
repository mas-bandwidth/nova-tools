package yield

import "syscall"

// currentNice is getpriority(PRIO_PROCESS, 0): darwin returns the nice value
// as it is.
func currentNice() (int, error) { return syscall.Getpriority(syscall.PRIO_PROCESS, 0) }

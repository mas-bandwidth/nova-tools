package yield

import "syscall"

// setNice is setpriority(PRIO_PROCESS, 0, n) on this process (the calling
// thread and, by inheritance, every process it starts).
func setNice(n int) error { return syscall.Setpriority(syscall.PRIO_PROCESS, 0, n) }

// currentNice is getpriority(PRIO_PROCESS, 0). The raw Linux system call
// answers 20 minus the nice value (so it never returns a negative), and Go's
// syscall.Getpriority hands that back untouched; this undoes it.
func currentNice() (int, error) {
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0, err
	}
	return 20 - raw, nil
}

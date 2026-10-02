package yield

import "syscall"

// Supported: darwin has setpriority, and a nice belongs to the process.
const Supported = true

// setNice is setpriority(PRIO_PROCESS, 0, n) on this process. A darwin nice
// belongs to the process, so a refusal to raise it is judged by the one
// reading of the process's nice (alreadyBehind), which covers every thread.
func setNice(n int) error {
	err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)
	if err == nil {
		return nil
	}
	if now, err2 := syscall.Getpriority(syscall.PRIO_PROCESS, 0); err2 == nil && alreadyBehind(err, now, n) {
		return nil
	}
	return err
}

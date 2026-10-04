//go:build unix

package friend

import (
	"os"
	"syscall"
)

// FlockHeld says whether another process holds an exclusive flock on path:
// the probe codex itself makes for a thread's writer lock (try_lock, and
// WouldBlock means an active writer). A path that does not exist is held
// by nobody.
func FlockHeld(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err == syscall.EWOULDBLOCK
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

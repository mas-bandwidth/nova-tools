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
	defer f.Close() // ignored: the lock file is closed after the flock is released; the OS drops the lock with the descriptor
	// A successful probe briefly owns LOCK_EX until the unlock below. It is
	// only an observation, not a reservation: another writer can win afterward.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err == syscall.EWOULDBLOCK
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // ignored: an unlock that fails leaves the flock to the descriptor's close, which drops it
	return false
}

// tryLock takes an exclusive flock on path, making the file when it is not there, and answers
// its release; ok false is another process holding it (two opens in one process are two
// holders too, as flock counts them by open file). A stage holds its job's lock while it runs
// (stage.go), so a second daemon beside it never stages the same job.
func tryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close() // ignored: a descriptor that holds nothing
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { f.Close() }, true, nil // ignored: the close drops the lock whatever it says
}

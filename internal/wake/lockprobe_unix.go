//go:build unix

package wake

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// On a build whose lock is flock -- internal/bus's lock_unix.go, build tag unix
// -- --lock <path> IS that file, opened and probed here.
//
// EACH FLAG IS HERE FOR A FAILURE IT PREVENTS (draft 4, finding 6):
// O_NOFOLLOW refuses a symlink at the lock path, because a lock whose path was
// replaced by a link into somebody else's file is not this lane's lock;
// O_NONBLOCK is what keeps the open of a FIFO from blocking BEFORE the
// non-blocking flock is ever reached, which is the way a watch could hang on a
// path with no deadline of its own; and the fstat is what makes the refusal a
// reading rather than a guess -- anything that is not a regular file is
// unreadable, no lock is attempted, and nothing is created, replaced or removed
// to learn it.
//
// A path replaced between two ticks is read as it NOW is: the value is the
// path's state and never an inode's.
func probeLock(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return "absent", nil
		case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.EMLINK):
			return "", errors.New("symlink at the lock path")
		}
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s at the lock path, not a regular file", kindOfMode(info.Mode()))
	}
	// The gap: taken non-blocking and released in the NEXT system call. The
	// tool never waits for it, never creates the file, never writes to it,
	// never deletes it, and never reads a pid out of it to test with kill.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return "held", nil
		}
		return "", err
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return "free", nil
}

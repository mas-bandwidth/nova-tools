//go:build !unix

package swarm

import (
	"errors"
	"os"
	"path/filepath"
)

// The lock where there is no flock: Windows, which this repo publishes a binary for. An
// exclusive create of a sibling file gives the same mutual exclusion and gives up the one
// property that makes flock better -- the kernel does not drop it when the holder dies, so
// a dispatcher killed holding it leaves the file behind and the next run refuses, naming
// it. That is a refusal a person can act on rather than a corruption, and it is written
// down here rather than discovered.
func tryLockFile(f *os.File) (bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission) {
			return false, nil
		}
		return false, err
	}
	// THE SENTINEL IS THE LOCK, so a close that failed never took one: returning
	// `(true, err)` had `TakeLock` report the lock untaken while the file stayed behind,
	// and the next run refused until a person cleared a lock nobody held (read 5,
	// finding 7). What was created here is removed here.
	if err := held.Close(); err != nil {
		_ = os.Remove(sentinel(f))
		return false, err
	}
	return true, nil
}

func unlockFile(f *os.File) { _ = os.Remove(sentinel(f)) }

func sentinel(f *os.File) string { return filepath.Clean(f.Name()) + ".held" }

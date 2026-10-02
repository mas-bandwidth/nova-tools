//go:build !unix

package swarm

import (
	"errors"
	"os"
	"path/filepath"
)

// On platforms without flock, an exclusive sibling-file create provides mutual exclusion.
// The sentinel remains when its holder exits, so a later attempt refuses instead of treating
// a possibly stale lock as available.
func tryLockFile(f *os.File) (bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission) {
			return false, nil
		}
		return false, err
	}
	// The sentinel is the lock, so a failed close must remove it before returning the error.
	if err := held.Close(); err != nil {
		// ignored: a cleanup on the failure path; the close error is the one returned
		_ = os.Remove(sentinel(f))
		return false, err
	}
	return true, nil
}

// ignored: unlock has no caller to report to; a leftover sentinel is refused and named by the next tryLockFile
func unlockFile(f *os.File) { _ = os.Remove(sentinel(f)) }

func sentinel(f *os.File) string { return filepath.Clean(f.Name()) + ".held" }

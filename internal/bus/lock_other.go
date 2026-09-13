//go:build !unix && !windows

package bus

import (
	"errors"
	"os"
	"path/filepath"
)

// The lock, where there is no flock and not Windows: generic exclusive create of a sibling file.
func tryLockFile(f *os.File) (bool, bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		return true, false, held.Close()
	}
	if errors.Is(err, os.ErrExist) {
		return false, true, nil
	}
	return false, false, err
}

func unlockFile(f *os.File) {
	_ = os.Remove(sentinel(f))
}

// sentinel is the file whose existence means the lock is held, beside the lock file the
// caller opened.
func sentinel(f *os.File) string {
	return filepath.Clean(f.Name()) + ".held"
}

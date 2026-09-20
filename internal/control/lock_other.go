//go:build !unix

package control

import (
	"errors"
	"os"
	"path/filepath"
)

// tryLockFile on non-unix platforms uses an exclusive sentinel file creation.
func tryLockFile(f *os.File) (bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission) {
			return false, nil
		}
		return false, err
	}
	if err := held.Close(); err != nil {
		_ = os.Remove(sentinel(f))
		return false, err
	}
	return true, nil
}

// unlockFile removes the sentinel file.
func unlockFile(f *os.File) {
	_ = os.Remove(sentinel(f))
}

func sentinel(f *os.File) string {
	return filepath.Clean(f.Name()) + ".held"
}

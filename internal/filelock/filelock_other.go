//go:build !unix && !windows

package filelock

import (
	"os"
	"time"
)

func unlockFile(f *os.File) {}

func openFileSafe(path string, flag int, perm os.FileMode) (*os.File, error) {
	return nil, ErrNotSupported
}

func tryLockWithOptions(path string, label string, opts options) (*FileLock, error) {
	return nil, ErrNotSupported
}

func lockWithOptions(path string, label string, timeout time.Duration, opts options) (*FileLock, error) {
	return nil, ErrNotSupported
}

func probeWithOptions(path string, opts options) (State, Stamp, error) {
	return "", Stamp{}, ErrNotSupported
}

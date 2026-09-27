//go:build !unix && !windows

package filelock

import (
	"os"
	"time"
)

func unlockFile(f *os.File) {}

// TryLockWithOptions returns ErrNotSupported on platforms without OS lock support.
func TryLockWithOptions(path string, label string, opts Options) (*FileLock, error) {
	return nil, ErrNotSupported
}

// LockWithOptions returns ErrNotSupported on platforms without OS lock support.
func LockWithOptions(path string, label string, timeout time.Duration, opts Options) (*FileLock, error) {
	return nil, ErrNotSupported
}

// ProbeWithOptions returns ErrNotSupported on platforms without OS lock support.
func ProbeWithOptions(path string, opts Options) (State, Stamp, error) {
	return "", Stamp{}, ErrNotSupported
}

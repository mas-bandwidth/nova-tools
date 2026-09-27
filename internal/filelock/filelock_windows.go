//go:build windows

package filelock

import (
	"os"
	"time"
)

// ProcessAlive reports whether pid names a running process on Windows.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p
	return false
}

// TryLock refuses with ErrNotSupported on Windows.
func TryLock(path string, label string) (*FileLock, error) {
	return nil, ErrNotSupported
}

// TryLockWithOptions refuses with ErrNotSupported on Windows.
func TryLockWithOptions(path string, label string, opts Options) (*FileLock, error) {
	return nil, ErrNotSupported
}

// Lock refuses with ErrNotSupported on Windows.
func Lock(path string, label string, timeout time.Duration) (*FileLock, error) {
	return nil, ErrNotSupported
}

// LockWithOptions refuses with ErrNotSupported on Windows.
func LockWithOptions(path string, label string, timeout time.Duration, opts Options) (*FileLock, error) {
	return nil, ErrNotSupported
}

// Unlock refuses with ErrNotSupported on Windows.
func (l *FileLock) Unlock() error {
	return ErrNotSupported
}

// Probe refuses with ErrNotSupported on Windows.
func Probe(path string) (State, Stamp, error) {
	return "", Stamp{}, ErrNotSupported
}

// ProbeWithOptions refuses with ErrNotSupported on Windows.
func ProbeWithOptions(path string, opts Options) (State, Stamp, error) {
	return "", Stamp{}, ErrNotSupported
}

// ClearStale refuses with ErrNotSupported on Windows.
func ClearStale(path string) error {
	return ErrNotSupported
}

// ClearStaleWithOptions refuses with ErrNotSupported on Windows.
func ClearStaleWithOptions(path string, opts Options) error {
	return ErrNotSupported
}

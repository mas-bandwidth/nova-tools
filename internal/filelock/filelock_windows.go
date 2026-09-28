//go:build windows

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
	lockOffsetHigh          = 0x80000000
)

func lockRange() *syscall.Overlapped {
	return &syscall.Overlapped{OffsetHigh: lockOffsetHigh}
}

func tryLockFile(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("nil file")
	}
	ov := lockRange()
	r, _, err := procLockFileEx.Call(f.Fd(), uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0, 1, 0, uintptr(unsafe.Pointer(ov)))
	if r != 0 {
		return true, nil
	}
	if err == errorLockViolation {
		return false, nil
	}
	return false, err
}

func trySharedLock(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("nil file")
	}
	ov := lockRange()
	r, _, err := procLockFileEx.Call(f.Fd(), uintptr(lockfileFailImmediately),
		0, 1, 0, uintptr(unsafe.Pointer(ov)))
	if r != 0 {
		return true, nil
	}
	if err == errorLockViolation {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	if f != nil {
		ov := lockRange()
		_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(ov)))
	}
}

// TryLockWithOptions attempts to acquire the exclusive file lock on path without waiting.
func TryLockWithOptions(path string, label string, opts Options) (*FileLock, error) {
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", path)
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("filelock %q: is a directory", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("filelock %q: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("filelock %q: %w", path, err)
	}

	// Refused is not yet held: see takeExclusive (tla/FileLock.tla, Blocked).
	if err := takeExclusive(f, path); err != nil {
		_ = f.Close()
		return nil, err
	}

	existing := readExistingStamp(f)
	var prev *Stamp
	if !existing.IsZero() {
		prev = &existing
	}

	if err := f.Truncate(0); err != nil {
		unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q truncate: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q seek: %w", path, err)
	}

	stamp := Stamp{
		PID:     opts.pid(),
		Host:    opts.host(),
		Started: opts.clock().Now().UTC(),
		Label:   label,
	}
	if _, err := f.WriteString(stamp.Format() + "\n"); err != nil {
		unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q write stamp: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q sync: %w", path, err)
	}

	return &FileLock{
		path:     path,
		file:     f,
		stamp:    stamp,
		previous: prev,
	}, nil
}

// LockWithOptions acquires the exclusive file lock on path, waiting up to timeout.
func LockWithOptions(path string, label string, timeout time.Duration, opts Options) (*FileLock, error) {
	return lockLoop(path, label, timeout, opts, TryLockWithOptions)
}

// ProbeWithOptions inspects path without taking an exclusive lock and without creating the file if absent.
func ProbeWithOptions(path string, opts Options) (State, Stamp, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StateAbsent, Stamp{}, nil
		}
		return "", Stamp{}, fmt.Errorf("filelock %q probe: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", Stamp{}, fmt.Errorf("filelock %q: symlink not permitted", path)
	}
	if fi.IsDir() {
		return "", Stamp{}, fmt.Errorf("filelock %q: is a directory", path)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StateAbsent, Stamp{}, nil
		}
		f, err = os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return StateAbsent, Stamp{}, nil
			}
			return "", Stamp{}, fmt.Errorf("filelock %q probe: %w", path, err)
		}
	}
	defer f.Close()

	ok, lockErr := trySharedLock(f)
	if lockErr != nil {
		return "", Stamp{}, fmt.Errorf("filelock %q probe: %w", path, lockErr)
	}
	if ok {
		unlockFile(f)
		return StateFree, Stamp{}, nil
	}

	holder := readExistingStamp(f)
	return StateHeld, holder, nil
}

//go:build windows

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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

func openFileSafe(path string, flag int, perm os.FileMode) (*os.File, error) {
	cleanPath := oneline.Escape(oneline.Cap(path, 1024))
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", cleanPath)
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("filelock %q: is a directory", cleanPath)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("filelock %q: not a regular file", cleanPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("filelock %q: %w", cleanPath, wrapPathError(err))
	}

	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, fmt.Errorf("filelock %q: %w", cleanPath, wrapPathError(err))
	}

	// Post-open verification: check f.Stat on open handle
	fiAfter, err := f.Stat()
	if err != nil {
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q stat: %w", cleanPath, wrapPathError(err))
	}
	if !fiAfter.Mode().IsRegular() {
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		if fiAfter.IsDir() {
			return nil, fmt.Errorf("filelock %q: is a directory", cleanPath)
		}
		if fiAfter.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", cleanPath)
		}
		return nil, fmt.Errorf("filelock %q: not a regular file", cleanPath)
	}

	// Post-open path check: verify path did not become a symlink/reparse point under open
	fiPost, err := os.Lstat(path)
	if err == nil {
		if fiPost.Mode()&os.ModeSymlink != 0 {
			// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
			_ = f.Close()
			return nil, fmt.Errorf("filelock %q: symlink not permitted", cleanPath)
		}
	}

	return f, nil
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
		// ignored: unlock has no caller to report to; the kernel lock is released when the handle closes
		_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(ov)))
	}
}

func tryLockWithOptions(path string, label string, opts options) (*FileLock, error) {
	cleanPath := oneline.Escape(oneline.Cap(path, 1024))
	syncFn := opts.getSync()

	f, err := openFileSafe(path, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}

	// Refused is not yet held: see takeExclusive (tla/FileLock.tla, Blocked).
	if err := takeExclusive(f, path); err != nil {
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
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
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q truncate: %w", cleanPath, wrapPathError(err))
	}
	if _, err := f.Seek(0, 0); err != nil {
		unlockFile(f)
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q seek: %w", cleanPath, wrapPathError(err))
	}

	stamp := Stamp{
		PID:     opts.getPID(),
		Host:    opts.getHost(),
		Started: opts.getClock().Now().UTC(),
		Label:   oneline.Cap(label, 1024),
	}
	if _, err := f.WriteString(stamp.Format() + "\n"); err != nil {
		unlockFile(f)
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q write stamp: %w", cleanPath, wrapPathError(err))
	}
	if err := syncFn(f); err != nil {
		unlockFile(f)
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q sync: %w", cleanPath, wrapPathError(err))
	}

	return &FileLock{file: f, previous: prev}, nil
}

func lockWithOptions(path string, label string, timeout time.Duration, opts options) (*FileLock, error) {
	return lockLoop(path, label, timeout, opts, tryLockWithOptions)
}

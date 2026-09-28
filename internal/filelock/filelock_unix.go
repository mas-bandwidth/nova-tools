//go:build unix

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func tryLockFile(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("nil file")
	}
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func trySharedLock(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("nil file")
	}
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	if f != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
}

func verifyInode(f *os.File, path string) (bool, error) {
	var statFd, statPath syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &statFd); err != nil {
		return false, err
	}
	if statFd.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return false, fmt.Errorf("filelock %q: not a regular file", path)
	}
	if err := syscall.Lstat(path, &statPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil // unlinked between open and lock
		}
		return false, err
	}
	if statPath.Mode&syscall.S_IFMT == syscall.S_IFLNK {
		return false, fmt.Errorf("filelock %q: symlink not permitted", path)
	}
	if statFd.Dev != statPath.Dev || statFd.Ino != statPath.Ino {
		return false, nil // replaced under open
	}
	return true, nil
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

	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
		if err != nil {
			return nil, fmt.Errorf("filelock %q: %w", path, err)
		}

		// Refused is not yet held: see takeExclusive (tla/FileLock.tla, Blocked).
		if err := takeExclusive(f, path); err != nil {
			_ = f.Close()
			return nil, err
		}

		// Inode race defense: verify fstat(fd) == lstat(path)
		match, err := verifyInode(f, path)
		if err != nil {
			unlockFile(f)
			_ = f.Close()
			return nil, err
		}
		if !match {
			// Inode changed under waiter: unlock, close, and retry.
			unlockFile(f)
			_ = f.Close()
			continue
		}

		// Read previous unreleased holder note if present.
		existing := readExistingStamp(f)
		var prev *Stamp
		if !existing.IsZero() {
			prev = &existing
		}

		// Truncate file, write own note, fsync.
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

	return nil, fmt.Errorf("filelock %q: failed after %d inode collision retries", path, maxRetries)
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

	// Open without creating!
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

	// Acquire non-blocking SHARED lock (LOCK_SH|LOCK_NB).
	// Probe NEVER takes an exclusive lock!
	ok, lockErr := trySharedLock(f)
	if lockErr != nil {
		return "", Stamp{}, fmt.Errorf("filelock %q probe: %w", path, lockErr)
	}
	if ok {
		// Granted shared lock: nobody holds exclusive lock.
		unlockFile(f)
		return StateFree, Stamp{}, nil
	}

	// Refused shared lock: an exclusive holder is present!
	holder := readExistingStamp(f)
	return StateHeld, holder, nil
}

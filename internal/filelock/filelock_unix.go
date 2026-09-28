//go:build unix

package filelock

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
)

func openFileSafe(path string, flag int, perm os.FileMode) (*os.File, error) {
	fullFlag := flag | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	f, err := os.OpenFile(path, fullFlag, perm)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", path)
		}
		return nil, fmt.Errorf("filelock %q: %w", path, err)
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q stat: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		if fi.IsDir() {
			return nil, fmt.Errorf("filelock %q: is a directory", path)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", path)
		}
		return nil, fmt.Errorf("filelock %q: not a regular file", path)
	}

	return f, nil
}

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
	const maxInodeRetries = 5
	for inodeAttempt := 0; inodeAttempt < maxInodeRetries; inodeAttempt++ {
		f, err := openFileSafe(path, os.O_RDWR|os.O_CREATE, 0666)
		if err != nil {
			return nil, err
		}

		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("filelock %q: %w", path, lockErr)
		}

		if !ok {
			// Exclusive lock refused. Re-ask algorithm (H1):
			// Check if a real exclusive holder is present or only transient shared askers.
			const probeRetries = 5
			var sharedSeen bool
			var acquiredOnRetry bool

			for retry := 0; retry < probeRetries; retry++ {
				shOk, shErr := trySharedLock(f)
				if shErr != nil {
					_ = f.Close()
					return nil, fmt.Errorf("filelock %q shared check: %w", path, shErr)
				}
				if !shOk {
					// Shared lock was also refused: an exclusive holder holds it!
					holder := readExistingStamp(f)
					_ = f.Close()
					return nil, &HeldError{Path: path, Holder: holder}
				}

				// Shared lock was granted: only shared askers (probers) were in the way.
				sharedSeen = true
				unlockFile(f)

				// Re-attempt exclusive lock immediately
				exOk, exErr := tryLockFile(f)
				if exErr != nil {
					_ = f.Close()
					return nil, fmt.Errorf("filelock %q: %w", path, exErr)
				}
				if exOk {
					acquiredOnRetry = true
					break
				}
				runtime.Gosched()
			}

			if !acquiredOnRetry {
				_ = f.Close()
				if sharedSeen {
					return nil, fmt.Errorf("filelock %q: %w", path, ErrBusy)
				}
				holder := readExistingStamp(f)
				return nil, &HeldError{Path: path, Holder: holder}
			}
		}

		// Acquired exclusive lock!
		// Inode race defense: verify fstat(fd) == lstat(path)
		match, err := verifyInode(f, path)
		if err != nil {
			unlockFile(f)
			_ = f.Close()
			return nil, err
		}
		if !match {
			// Inode changed under waiter: unlock, close, and retry opening path.
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

	return nil, fmt.Errorf("filelock %q: failed after %d inode collision retries", path, maxInodeRetries)
}

// LockWithOptions acquires the exclusive file lock on path, waiting up to timeout.
func LockWithOptions(path string, label string, timeout time.Duration, opts Options) (*FileLock, error) {
	return lockLoop(path, label, timeout, opts, TryLockWithOptions)
}

// ProbeWithOptions inspects path without taking an exclusive lock and without creating the file if absent.
func ProbeWithOptions(path string, opts Options) (State, Stamp, error) {
	f, err := openFileSafe(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StateAbsent, Stamp{}, nil
		}
		// If write permission denied, try read-only
		f, err = openFileSafe(path, os.O_RDONLY, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return StateAbsent, Stamp{}, nil
			}
			return "", Stamp{}, err
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

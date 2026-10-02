//go:build unix

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func openFileSafe(path string, flag int, perm os.FileMode) (*os.File, error) {
	cleanPath := oneline.Escape(oneline.Cap(path, 1024))
	fullFlag := flag | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	f, err := os.OpenFile(path, fullFlag, perm)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", cleanPath)
		}
		return nil, fmt.Errorf("filelock %q: %w", cleanPath, wrapPathError(err))
	}

	fi, err := f.Stat()
	if err != nil {
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		return nil, fmt.Errorf("filelock %q stat: %w", cleanPath, wrapPathError(err))
	}
	if !fi.Mode().IsRegular() {
		// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
		_ = f.Close()
		if fi.IsDir() {
			return nil, fmt.Errorf("filelock %q: is a directory", cleanPath)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("filelock %q: symlink not permitted", cleanPath)
		}
		return nil, fmt.Errorf("filelock %q: not a regular file", cleanPath)
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
		// ignored: unlock has no caller to report to; the kernel lock is released when the descriptor closes
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
}

func verifyInode(f *os.File, path string) (bool, error) {
	cleanPath := oneline.Escape(oneline.Cap(path, 1024))
	var statFd, statPath syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &statFd); err != nil {
		return false, wrapPathError(err)
	}
	if statFd.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return false, fmt.Errorf("filelock %q: not a regular file", cleanPath)
	}
	if err := syscall.Lstat(path, &statPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil // unlinked between open and lock
		}
		return false, wrapPathError(err)
	}
	if statPath.Mode&syscall.S_IFMT == syscall.S_IFLNK {
		return false, fmt.Errorf("filelock %q: symlink not permitted", cleanPath)
	}
	if statFd.Dev != statPath.Dev || statFd.Ino != statPath.Ino {
		return false, nil // replaced under open
	}
	return true, nil
}

func (o options) getVerifyInode() func(*os.File, string) (bool, error) {
	if o.verifyInode != nil {
		return o.verifyInode
	}
	return verifyInode
}

func tryLockWithOptions(path string, label string, opts options) (*FileLock, error) {
	cleanPath := oneline.Escape(oneline.Cap(path, 1024))
	const maxInodeRetries = 5
	verifyFn := opts.getVerifyInode()
	syncFn := opts.getSync()

	for inodeAttempt := 0; inodeAttempt < maxInodeRetries; inodeAttempt++ {
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

		// Acquired exclusive lock!
		// Inode race defense: verify fstat(fd) == lstat(path)
		match, err := verifyFn(f, path)
		if err != nil {
			unlockFile(f)
			// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
			_ = f.Close()
			return nil, err
		}
		if !match {
			// Inode changed under waiter: unlock, close, and retry opening path.
			unlockFile(f)
			// ignored: a close on the failure path; the error returned says what went wrong, and the close drops any kernel lock
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

	return nil, fmt.Errorf("filelock %q: failed after %d inode collision retries", cleanPath, maxInodeRetries)
}

func lockWithOptions(path string, label string, timeout time.Duration, opts options) (*FileLock, error) {
	return lockLoop(path, label, timeout, opts, tryLockWithOptions)
}

func probeWithOptions(path string, opts options) (State, Stamp, error) {
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
		cleanPath := oneline.Escape(oneline.Cap(path, 1024))
		return "", Stamp{}, fmt.Errorf("filelock %q probe: %w", cleanPath, wrapPathError(lockErr))
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

//go:build unix

package filelock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

// ProcessAlive reports whether pid names a running process.
// Signal 0 performs existence and permission checks without delivering a signal.
// EPERM means the process exists and is running, but belongs to another user.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// TryLock attempts to acquire an exclusive lock on path immediately without waiting.
// If the lock is held by another live process, it returns a *HeldError wrapping ErrHeld.
// If the lock file exists and names a dead process, it returns a *StaleError wrapping ErrStale.
func TryLock(path string, label string) (*FileLock, error) {
	return TryLockWithOptions(path, label, Options{})
}

// TryLockWithOptions attempts to acquire an exclusive lock on path with custom options.
func TryLockWithOptions(path string, label string, opts Options) (*FileLock, error) {
	return lockInternal(path, label, 0, opts)
}

// Lock waits up to timeout to acquire an exclusive lock on path, with jitter between attempts.
// If timeout is 0, it attempts acquisition once without waiting.
// If the deadline expires while held, it returns a *HeldError wrapping ErrHeld.
// If the lock is stale, it returns a *StaleError wrapping ErrStale and refuses to steal it silently.
func Lock(path string, label string, timeout time.Duration) (*FileLock, error) {
	return LockWithOptions(path, label, timeout, Options{})
}

// LockWithOptions waits up to timeout with custom options (such as an injected Clock).
func LockWithOptions(path string, label string, timeout time.Duration, opts Options) (*FileLock, error) {
	return lockInternal(path, label, timeout, opts)
}

func lockInternal(path string, label string, timeout time.Duration, opts Options) (*FileLock, error) {
	clk := opts.clock()
	poll := opts.pollInterval()

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("filelock %s could not be opened: %w", path, err)
	}

	deadline := clk.Now().Add(timeout)
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("filelock %s: %w", path, lockErr)
		}
		if ok {
			existingStamp := readExistingStamp(f)
			if existingStamp.PID > 0 && !opts.isAlive(existingStamp.PID) {
				unlockFile(f)
				_ = f.Close()
				return nil, &StaleError{Path: path, Holder: existingStamp}
			}

			newStamp := Stamp{
				PID:     os.Getpid(),
				Host:    opts.host(),
				Started: clk.Now().UTC(),
				Label:   label,
			}
			if err := stampHolder(f, newStamp); err != nil {
				unlockFile(f)
				_ = f.Close()
				return nil, fmt.Errorf("filelock %s stamp: %w", path, err)
			}
			return &FileLock{
				path:  path,
				file:  f,
				stamp: newStamp,
			}, nil
		}

		// Lock is held by another process.
		holderStamp, _ := ReadStamp(path)
		if holderStamp.PID > 0 && !opts.isAlive(holderStamp.PID) {
			_ = f.Close()
			return nil, &StaleError{Path: path, Holder: holderStamp}
		}

		if timeout == 0 || !clk.Now().Before(deadline) {
			_ = f.Close()
			return nil, &HeldError{Path: path, Holder: holderStamp, Wait: timeout}
		}

		clk.Sleep(opts.jitter(poll))
	}
}

// Unlock releases the exclusive lock and closes the underlying file descriptor.
// It is idempotent and safe to call multiple times.
func (l *FileLock) Unlock() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	if l.file == nil {
		return nil
	}
	_ = l.file.Truncate(0)
	_ = l.file.Sync()
	unlockFile(l.file)
	return l.file.Close()
}

// Probe inspects the state of the lock file at path without blocking and without taking the lock.
// It NEVER creates the file if absent.
// It returns StateAbsent, StateFree, StateHeld, or StateStale, along with any parsed Stamp.
func Probe(path string) (State, Stamp, error) {
	return ProbeWithOptions(path, Options{})
}

// ProbeWithOptions inspects the state of the lock file at path using custom options.
func ProbeWithOptions(path string, opts Options) (State, Stamp, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return StateAbsent, Stamp{}, nil
		case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.EMLINK):
			return "", Stamp{}, errors.New("symlink at the lock path")
		}
		return "", Stamp{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", Stamp{}, err
	}
	if !info.Mode().IsRegular() {
		return "", Stamp{}, fmt.Errorf("%s at the lock path, not a regular file", kindOfMode(info.Mode()))
	}

	raw, err := io.ReadAll(f)
	if err != nil {
		return "", Stamp{}, err
	}
	stamp, _ := ParseStamp(string(raw))

	if stamp.PID > 0 && !opts.isAlive(stamp.PID) {
		return StateStale, stamp, nil
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return StateHeld, stamp, nil
		}
		return "", Stamp{}, err
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return StateFree, stamp, nil
}

// ClearStale removes the lock file at path if and only if it is determined to be Stale.
// If the lock is Held, Free, or Absent, ClearStale refuses with an error.
func ClearStale(path string) error {
	return ClearStaleWithOptions(path, Options{})
}

// ClearStaleWithOptions removes the lock file at path with custom options.
func ClearStaleWithOptions(path string, opts Options) error {
	state, _, err := ProbeWithOptions(path, opts)
	if err != nil {
		return fmt.Errorf("filelock clear stale %s: %w", path, err)
	}
	if state != StateStale {
		return fmt.Errorf("filelock clear stale %s: lock is %s, not stale", path, state)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("filelock clear stale %s: %w", path, err)
	}
	defer f.Close()

	ok, lockErr := tryLockFile(f)
	if lockErr != nil {
		return fmt.Errorf("filelock clear stale %s: %w", path, lockErr)
	}
	if !ok {
		return fmt.Errorf("filelock clear stale %s: lock is currently held by a live process", path)
	}
	defer unlockFile(f)

	raw, _ := io.ReadAll(f)
	st, _ := ParseStamp(string(raw))
	if st.PID > 0 && opts.isAlive(st.PID) {
		return fmt.Errorf("filelock clear stale %s: holder is alive", path)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filelock clear stale %s: %w", path, err)
	}
	return nil
}

func tryLockFile(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("file is nil")
	}
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func readExistingStamp(f *os.File) Stamp {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Stamp{}
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return Stamp{}
	}
	st, _ := ParseStamp(string(raw))
	return st
}

func stampHolder(f *os.File, stamp Stamp) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := f.WriteString(stamp.Format()); err != nil {
		return err
	}
	return f.Sync()
}

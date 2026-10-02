//go:build !unix

package tokens

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// takeFold where there is no flock: lock_other.go's exclusive create of a sibling file,
// polled until wait runs out, with the holder's pid written into the lock file. This is
// the loop every platform ran before the unix build moved onto internal/filelock; it stays
// as it was until a migration in which an old and a new binary on one machine still
// exclude each other (this build locks the sibling file; filelock's is LockFileEx on the
// file itself).
func takeFold(path string, wait time.Duration) (func(), bool, error) {
	f, err := openFoldLock(path)
	if err != nil {
		return nil, false, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	jitter := time.Duration(os.Getpid()%17) * time.Millisecond
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			return nil, false, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
			// ignored: the pid stamp is advice for a reader of a held lock; the lock itself is the lock
			_ = f.Truncate(0)
			// ignored: the pid stamp is advice for a reader of a held lock; the lock itself is the lock
			_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
			return func() {
				unlockFile(f)
				f.Close()
			}, false, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, true, nil
		}
		time.Sleep(lockPoll + jitter)
	}
}

// lockPoll is the retry interval, jittered by the caller's own pid so that two waiters do
// not step in lockstep.
const lockPoll = 50 * time.Millisecond

// openFoldLock creates a new lock exclusively or opens an existing regular file.
// An existing path is never opened with O_CREATE: a dangling symlink cannot make
// a file elsewhere. Validate the opened file against the directory entry before
// the caller locks or writes it.
func openFoldLock(path string) (*os.File, error) {
	f, err := openFoldLockFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if os.IsExist(err) {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("lock path is a symlink; use a regular fold.lock file")
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("lock path is not a regular file; use a regular fold.lock file")
		}
		f, err = openFoldLockFile(path, os.O_RDWR)
	}
	if err != nil {
		return nil, err
	}
	info, statErr := f.Stat()
	entry, entryErr := os.Lstat(path)
	if statErr != nil || entryErr != nil || !info.Mode().IsRegular() || !entry.Mode().IsRegular() || !os.SameFile(info, entry) {
		f.Close()
		return nil, fmt.Errorf("lock path changed or is not a regular file; use a stable regular fold.lock file")
	}
	return f, nil
}

// HolderPID is the pid inside the lock file, or a dash when it holds none. It is read
// rather than scanned for: this tool never looks at another process's command line.
func HolderPID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dash
	}
	pid := strings.TrimSpace(string(raw))
	if pid == "" {
		return Dash
	}
	if _, err := strconv.Atoi(pid); err != nil {
		return Dash
	}
	return pid
}

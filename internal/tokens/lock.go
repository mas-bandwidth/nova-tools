package tokens

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// One fold per output directory.
//
// The day file is written whole and atomically via internal/atomicfile. A fold
// takes a kernel lock on <out>/fold.lock and holds it to the end; the second waits
// a bounded, jittered time and then refuses, naming the holder's pid so a person
// can see what to wait for or kill.
//
// It is an flock rather than a file whose existence means "held", for the reason
// internal/bus gives: the kernel drops it when the process dies, so a fold killed mid-run
// leaves nothing for the next one to clear. The pid is written INSIDE the locked file so
// the name in the refusal is the holder's own, never a stale sentinel's.

// LockName is the lock file, inside the output directory: the thing being protected is
// that directory's day files, so the lock belongs beside them.
const LockName = "fold.lock"

// LockWait is how long a second fold waits before refusing. It is bounded on purpose: a
// wait with no end is a tool that has stopped saying anything.
const LockWait = 10 * time.Second

// lockPoll is the retry interval, jittered by the caller's own pid so that two waiters do
// not step in lockstep.
const lockPoll = 50 * time.Millisecond

// TakeFoldLock takes the output directory's lock, waiting up to wait, and returns the
// release. The release is safe to call more than once.
func TakeFoldLock(out string, wait time.Duration) (func(), error) {
	path := filepath.Join(out, LockName)
	if err := checkOutputDirectory(out); err != nil {
		return nil, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	f, err := openFoldLock(path)
	if err != nil {
		return nil, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	jitter := time.Duration(os.Getpid()%17) * time.Millisecond
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
			// ignored: the pid stamp is advice for a reader of a held lock; the flock itself is the lock
			_ = f.Truncate(0)
			// ignored: the pid stamp is advice for a reader of a held lock; the flock itself is the lock
			_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
			released := false
			return func() {
				if released {
					return
				}
				released = true
				unlockFile(f)
				f.Close()
			}, nil
		}
		if !time.Now().Before(deadline) {
			held := HolderPID(path)
			f.Close()
			return nil, fmt.Errorf("another nova-tokens fold holds %s (pid %s); this run waited %s and will not write beside it, because two folds on one --out would race to replace the same day files", path, held, wait)
		}
		time.Sleep(lockPoll + jitter)
	}
}

// openFoldLock creates a new lock exclusively or opens an existing regular file.
// An existing path is never opened with O_CREATE: a dangling symlink cannot make
// a file elsewhere. Validate the opened file against the directory entry before
// the caller locks or writes it. Unix also refuses symlinks at the open itself.
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

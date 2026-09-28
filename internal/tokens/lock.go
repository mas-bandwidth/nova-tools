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
// The day file is written atomically via internal/atomicfile (rule 8). A fold
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
// release. The release is safe to call more than once. out that is a symlink, or whose
// parent is a symlink, is refused and the lock file is not created.
func TakeFoldLock(out string, wait time.Duration) (func(), error) {
	// OpenFile follows a directory symlink and creates fold.lock in the referent.
	// The release does not remove it. On macOS, Lstat of "link/" reports the
	// directory, and Dir of "parent-link/child/" is "parent-link/child", so a
	// trailing separator is cleaned for these Lstats only. The lock is still
	// opened on out as given.
	probed := out
	if len(out) > 0 && os.IsPathSeparator(out[len(out)-1]) {
		probed = filepath.Clean(out)
	}
	if fi, err := os.Lstat(probed); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, symlinkedFoldOut(probed, out)
	}
	if parent := filepath.Dir(probed); parent != probed {
		if fi, err := os.Lstat(parent); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, symlinkedFoldOut(parent, out)
		}
	}
	path := filepath.Join(out, LockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
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
			_ = f.Truncate(0)
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
			return nil, fmt.Errorf("another nova-tokens fold holds %s (pid %s); this run waited %s and will not write beside it, because two folds on one --out write one temp name", path, held, wait)
		}
		time.Sleep(lockPoll + jitter)
	}
}

// symlinkedFoldOut names the lock, the symlink, the path that was passed, and
// the next action. symlink is the link itself; path is out as given, which may
// be that link or a child reached through it, with a trailing separator kept.
func symlinkedFoldOut(symlink, path string) error {
	return fmt.Errorf("the lock %s: %q is a symlink; path %q; next: pass the real directory", LockName, symlink, path)
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

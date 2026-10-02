package tokens

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// One fold per output directory.
//
// The day file is written atomically via internal/atomicfile (rule 8). A fold
// takes internal/filelock's kernel lock (tla/FileLock.tla) on <out>/fold.lock and
// holds it to the end; the second waits a bounded, jittered time and then refuses,
// naming the holder's pid so a person can see what to wait for or kill.
//
// It is a kernel lock rather than a file whose existence means "held", for the reason
// internal/bus gives: the kernel drops it when the process dies, so a fold killed mid-run
// leaves nothing for the next one to clear. The holder's stamp is written INSIDE the
// locked file so the name in the refusal is the holder's own, never a stale sentinel's.
// filelock refuses a lock path that is a symlink, a directory or not a regular file, and
// one replaced between its open and its lock.

// LockName is the lock file, inside the output directory: the thing being protected is
// that directory's day files, so the lock belongs beside them.
const LockName = "fold.lock"

// LockWait is how long a second fold waits before refusing. It is bounded on purpose: a
// wait with no end is a tool that has stopped saying anything.
const LockWait = 10 * time.Second

// TakeFoldLock takes the output directory's lock, waiting up to wait, and returns the
// release. The release is safe to call more than once.
func TakeFoldLock(out string, wait time.Duration) (func(), error) {
	path := filepath.Join(out, LockName)
	if err := checkOutputDirectory(out); err != nil {
		return nil, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	l, err := filelock.Lock(path, "nova-tokens fold", wait)
	if errors.Is(err, filelock.ErrHeld) || errors.Is(err, filelock.ErrBusy) {
		return nil, fmt.Errorf("another nova-tokens fold holds %s (pid %s); this run waited %s and will not write beside it, because two folds on one --out would race to replace the same day files", path, HolderPID(path), wait)
	}
	if err != nil {
		return nil, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	// ignored: release has no caller to report to; the kernel lock goes with the descriptor either way
	return func() { _ = l.Unlock() }, nil
}

// HolderPID is the pid in the lock file's holder stamp, or a dash when it holds none. It
// is read rather than scanned for: this tool never looks at another process's command line.
func HolderPID(path string) string {
	st, err := filelock.ReadStamp(path)
	if err != nil || st.PID <= 0 {
		return Dash
	}
	return strconv.Itoa(st.PID)
}

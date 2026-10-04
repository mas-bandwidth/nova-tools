package tokens

import (
	"fmt"
	"path/filepath"
	"time"
)

// One fold per output directory.
//
// The day file is written atomically via internal/atomicfile. Each fold takes a
// kernel lock on <out>/fold.lock and holds it to the end; a second fold waits
// a bounded, jittered time and then refuses, naming the holder's pid so a person
// can see what to wait for or kill.
//
// It is an flock rather than a file whose existence means "held", for the reason
// internal/bus gives: the kernel drops it when the process dies, so a fold killed mid-run
// leaves nothing for the next one to clear. The pid is written INSIDE the locked file so
// the name in the refusal is the holder's own, never a stale sentinel's. On unix the lock
// is internal/filelock's (tla/FileLock.tla), the same flock on the same file the earlier
// binaries took, so an old and a new fold still exclude each other during an upgrade
// (lock_compat_unix_test.go); elsewhere it is still lock_other.go's.

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
	unlock, held, err := takeFold(path, wait)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, fmt.Errorf("another nova-tokens fold holds %s (pid %s); this run waited %s and will not write beside it, because two folds on one --out would race to replace the same day files", path, HolderPID(path), wait)
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		unlock()
	}, nil
}

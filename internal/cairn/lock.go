package cairn

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// lockFileName is the one file the tool adds beside a store's records: the lock
// that makes a check-then-write one critical section. It is empty when free and
// is never deleted, and it is neither a session file nor a shape marker. A
// store kept in git lists it in its ignore file (.cairn.lock).
const lockFileName = ".cairn.lock"

// The lock serialises writers on ONE machine. Two machines writing one store
// synced through git are outside it: each appends to its own session file, and
// concurrent appends to the same session from two machines conflict in git,
// which is visible.
const (
	lockWait = 10 * time.Second
	lockPoll = 25 * time.Millisecond
)

// lockClock is the time the wait is measured in, so a test can run the whole
// wait without sleeping.
type lockClock struct {
	now   func() time.Time
	sleep func(time.Duration)
	poll  time.Duration
}

var realClock = lockClock{now: time.Now, sleep: time.Sleep, poll: lockPoll}

// LockedError is the refusal for a store whose lock another process holds past
// the wait, or whose lock file cannot be made. It names the cause and the next
// action.
type LockedError struct{ Msg string }

func (e *LockedError) Error() string { return e.Msg }

// withStoreLock runs fn holding the store's exclusive lock, so that reading what
// an entry id already holds, deciding, and writing are one step no other writer
// on this machine can interleave with (tla/CairnStore.tla: Begin takes the lock,
// Read decides and Finish writes, and the nolock witness is the variant where
// two writers both read "new" and both write). The lock is internal/filelock's;
// the wait is bounded, and on timeout the refusal names the holder. A store
// directory that does not exist has nothing to protect and fn runs as it is.
func withStoreLock(op, store string, fn func() error) error {
	return withStoreLockOn(realClock, lockWait, op, store, fn)
}

func withStoreLockOn(c lockClock, wait time.Duration, op, store string, fn func() error) error {
	if info, err := os.Stat(store); err != nil || !info.IsDir() {
		return fn()
	}
	path := filepath.Join(store, lockFileName)
	deadline := c.now().Add(wait)
	for {
		lock, err := filelock.TryLock(path, "nova-cairn "+op)
		if err == nil {
			defer lock.Unlock()
			return fn()
		}
		held, isHeld := filelock.AsHeldError(err)
		if !isHeld && !errors.Is(err, filelock.ErrBusy) {
			return lockFileError(op, store, path, err)
		}
		if !c.now().Before(deadline) {
			who := "another process"
			if isHeld && !held.Holder.IsZero() {
				who = held.Holder.String()
			}
			return &LockedError{Msg: fmt.Sprintf("cannot %s: %s holds the lock on store %q and did not let go in %s; "+
				"run the same command again once it finishes (the lock is released when that process exits)",
				opPhrase(op), oneline.Escape(who), store, wait)}
		}
		c.sleep(c.poll)
	}
}

// lockFileError is the refusal for a lock file that cannot be made or opened,
// naming the cause and the next action.
func lockFileError(op, store, path string, err error) error {
	// Something that is not a plain file stands at the lock's name: making the
	// directory writable changes nothing, moving that path does.
	if li, lerr := os.Lstat(path); lerr == nil && !li.Mode().IsRegular() {
		what := "not a regular file"
		if isLink(li) {
			what = "a symbolic link"
		} else if li.IsDir() {
			what = "a directory"
		}
		return &LockedError{Msg: fmt.Sprintf("cannot %s: the lock file %q is %s; move or remove that path, then run the same command again",
			opPhrase(op), path, what)}
	}
	switch {
	case errors.Is(err, fs.ErrPermission):
		return &LockedError{Msg: fmt.Sprintf("cannot %s: cannot create the lock file %q: permission denied on %q; "+
			"make the store directory writable, or write from an account that can", opPhrase(op), path, store)}
	}
	return &LockedError{Msg: fmt.Sprintf("cannot %s: cannot take the lock %q: %v; "+
		"make the store directory writable and run the same command again", opPhrase(op), path, err)}
}

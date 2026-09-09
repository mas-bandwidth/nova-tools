package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// One nova-bus per checkout.
//
// THE FAILURE THIS CLOSES. Every verb here works by reading the checkout, writing into it,
// and asking git about it -- and two invocations on ONE checkout interleave all three. Two
// `inbox --advance` runs read the same OPEN list and each writes it back without the
// other's work; a `send` mid-commit is a dirty tree the other one refuses over, or worse,
// stages; a rebase started by one is a checkout the other finds detached. None of those is
// a race the tool can win by being careful, because the shared state is a directory rather
// than a variable, and none of them is what the push protocol's retry is for: that is two
// benches, on two checkouts, which is the case this tool was built for and handles. This is
// two of ME, on one checkout, which is a mistake and should say so.
//
// So a run takes a lock on the checkout and holds it to the end. The second one WAITS --
// briefly, because the first is usually a fetch away from finishing -- and then refuses
// with a sentence rather than corrupting anything.
//
// The lock is an flock on a file inside the git directory. Inside the git directory because
// that is per-CHECKOUT: two linked worktrees of one repository are two checkouts and must
// not block each other, and a lock at the table root would be a file on the table that
// every reader would then have to know is not a note. An flock rather than an O_EXCL
// sentinel because the kernel drops it when the process dies: a run killed with the lock
// held leaves nothing for the next one to clear, which is the failure mode that makes
// lock-file schemes worse than no lock at all.

// LockName is the lock file, in the checkout's git directory.
const LockName = "nova-bus.lock"

// LockCheckout takes this checkout's lock, waiting up to wait for it, and returns the
// release. The release is safe to call more than once.
//
// A table that is not a git checkout at all has nothing to lock and is not locked: the only
// verbs that reach one are the full reads, which write nothing, and refusing them for the
// want of a `.git` would be refusing a table for a reason that is not about the table.
func LockCheckout(table string, wait time.Duration) (func(), error) {
	gd, err := GitDir(table)
	if err != nil {
		return func() {}, nil
	}
	path := filepath.Join(gd, LockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("the lock that keeps two runs off one checkout could not be opened at %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
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
			f.Close()
			return nil, fmt.Errorf("another nova-bus is already running on this checkout and still holds %s; this run waited %s for it and will not work beside it, because two runs on one checkout write one OPEN list and one index -- run this again when that one has finished", path, wait)
		}
		time.Sleep(lockPoll)
	}
}

// lockPoll is how often the wait re-tries. It is a poll rather than a blocking flock
// because the wait is BOUNDED: a blocking take would give the refusal no way to happen, and
// the refusal is the point.
const lockPoll = 25 * time.Millisecond

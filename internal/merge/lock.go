package merge

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

// The lane's locks, and the whole of rule 2 in one sentence: THE KERNEL RELEASES THE LOCK
// WHEN ITS HOLDER DIES, so there is nothing to break and no age to compute.
//
// The prototype's lock was a directory holding a pid with a stale rule, and its stale
// arithmetic read a lock that had vanished between the existence test and the stat as
// infinitely old, called it stale, and broke the lock the next writer had just taken: 3
// of 20 concurrent writes were lost on 2026-09-11. That whole class of bug is what the
// kernel lock removes, and it is why there is no --force-unlock, no age and no sweeper
// anywhere in this tool.
//
// A second holder WAITS a bounded, jittered time (lesson 66 -- unjittered waiters
// synchronise and collide again) and then exits 2 naming the holder's pid, which the
// holder wrote inside the file after taking it.

// LockWait is the DEFAULT bounded wait, and it is the default of the --timeout flag and
// nothing else: rule 2 says a verb that cannot take the lock within ITS --timeout exits 2
// and says who holds it, so every verb passes its own --timeout and this constant is what
// a caller with no flag to pass -- a test, a helper -- uses. It is a wait for one
// read-modify-write of a JSON file, which is microseconds of work, so ten seconds is a
// holder that has died in a way the kernel could not see or a machine under a load the
// caller wants to hear about.
const LockWait = 10 * time.Second

// lockPoll is how often the wait re-tries. A poll rather than a blocking take because the
// wait is BOUNDED and the refusal is the point.
const lockPoll = 25 * time.Millisecond

// Lock takes the kernel lock on path, waiting up to wait, and returns the release. The
// release is safe to call more than once.
//
// The holder writes `pid=<n> at=<stamp>` into the file after taking it, so a waiter that
// runs out has something to name. That write is not the lock and is never read as one: a
// file holding a pid whose process is gone is a file, and the kernel is what says whether
// anybody holds this.
func Lock(path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("the lock at %s could not be opened: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
			stampHolder(f)
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
			held := holder(path)
			f.Close()
			return nil, &HeldError{Path: path, Holder: held, Wait: wait}
		}
		time.Sleep(jitter(lockPoll))
	}
}

// HeldError is a bounded wait that ran out: somebody else holds this lane's lock. It is
// its own type because the verb's answer to it is fixed by rule 2 -- "a verb that cannot
// take the lock within its --timeout exits 2 and says who holds it" -- and exit 2 is a
// different answer from every other failure a Git operation can have.
type HeldError struct {
	Path   string
	Holder string
	Wait   time.Duration
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("another nova-merge holds %s (%s); this run waited %s for it, and two writers of one lane's state is not a race care can win -- run this again when that one has finished", e.Path, e.Holder, e.Wait)
}

// AsHeldError reports whether err is a lock this run could not take, for the exit code.
func AsHeldError(err error) (*HeldError, bool) {
	var h *HeldError
	ok := errors.As(err, &h)
	return h, ok
}

// jitter spreads the retries of several waiters so they do not wake together and collide
// again (lesson 66). The spread is up to half the poll, which is enough to break the
// lockstep and small enough that the bounded wait means what it says.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

// stampHolder writes who holds this lock, for the refusal a waiter prints. A failure to
// write it is not a failure to hold the lock: the lock is the kernel's.
func stampHolder(f *os.File) {
	line := fmt.Sprintf("pid=%d at=%s\n", os.Getpid(), time.Now().UTC().Format(Stamp))
	if err := f.Truncate(0); err != nil {
		return
	}
	if _, err := f.Seek(0, 0); err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Sync()
}

// holder reads the line the holder left. It reports what it found and never infers
// anything from it -- in particular it does not ask whether that pid is alive, because
// this tool has no stale rule to feed the answer to.
func holder(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "the holder left no pid"
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return "the holder left no pid"
	}
	return line
}

// HolderPID is the pid a lock file names, or 0. It is exported for the refusal that says
// who holds the pass lock, and it is the only thing this tool ever reads out of a lock.
func HolderPID(path string) int {
	for _, tok := range strings.Fields(holder(path)) {
		if v, ok := strings.CutPrefix(tok, "pid="); ok {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}

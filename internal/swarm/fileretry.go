package swarm

import (
	"errors"
	"io/fs"
	"os"
	"time"
)

// THE ATOMIC-REPLACE COLLISION, and the one place it is answered.
//
// Every durable record this package writes goes down through `writeAtomic`: a .tmp, an
// fsync, and a rename over the destination. On unix that rename is atomic against every
// reader and there is nothing here to do. On WINDOWS it is not: a file opened for reading
// is opened WITHOUT FILE_SHARE_DELETE by the Go runtime, so
//
//   * a rename over a path a reader has open fails with ERROR_SHARING_VIOLATION (32), and
//   * a read of a path whose old file is in delete-pending fails with ERROR_ACCESS_DENIED (5).
//
// Both are TRANSIENT: they say "somebody else is at this path right now", and they say
// nothing whatever about the job whose record it is. Read as facts, they cost two flakes:
//
//   #92, `no identity within 10s`: the supervisor's Identify rename lost to the
//   dispatcher's own handshake loop, which reads that slot file every 20ms. Identify
//   returned "the slot file is gone or unreadable", the supervisor ABORTED, and the
//   dispatcher then waited out its whole launch timeout for an identity nobody was left
//   to write.
//
//   run 34698330796, `TestANumericBudgetWithNoUsageSourceIsRefused`: the reverse
//   direction. The supervisor rewrites its own slot file (UpdateSlot, to record the job's
//   process group) milliseconds after it identifies -- which is exactly when the
//   dispatcher's first poll of that same file lands. One unreadable poll and `state` read
//   a RUNNING job as dead: the dispatcher reaped a live supervisor at `after=0s`, found no
//   exit.json where it had just killed the process that writes it, and printed
//   `RUN DONE … rc=-1 … dest=failed` with `result=ok findings=1` from the report the
//   worker had already published. `end=unknown` makes the pass exit 1.
//
// So the collision is absorbed HERE, in the two operations that meet, and the clock is the
// OUTER BOUND of a wait for an observable -- never the thing being raced. Every retry ends
// on its own (a wait loop always has a deadline), and on a platform where these errors do
// not exist `transientIO` is false and not one of these loops ever runs a second turn.

// SteadyWindow is how long a read or a rename waits out somebody else at the same path. It
// is a property of the FILESYSTEM's replace window -- the microseconds a delete is pending
// -- and not of anybody's job, so it is short, and long enough that a scheduler that parks
// this goroutine for a quantum still lands inside it.
const SteadyWindow = 2 * time.Second

const steadyPoll = 5 * time.Millisecond

// THE WINDOW IS A CEILING, NEVER AN ADDITION TO SOMEBODY ELSE'S CLOCK (Stella, #126).
//
// SteadyWindow is what ONE collision may cost. A caller that retries -- the launch
// handshake reads a slot file every 20ms for its whole launch timeout -- would otherwise
// pay it once per turn: 500 reads x 2s is 1010s spent inside a 10s bound, with run.lock
// held the entire time. So every caller that has a bound of its own hands it down, and the
// wait here ends at min(its own window, the caller's remaining budget). A caller with no
// bound passes the zero time and gets the window, as before.
func steadyDeadline(budget time.Time) time.Time {
	own := time.Now().Add(SteadyWindow)
	if budget.IsZero() || own.Before(budget) {
		return own
	}
	return budget
}

// FileRetry encapsulates the collision wait: an atomic replace collision (transient
// ERROR_SHARING_VIOLATION or ERROR_ACCESS_DENIED on Windows) is waited out until its
// deadline. In tests, Transient may be set to simulate transient errors without a global seam.
type FileRetry struct {
	Transient func(error) bool
}

func (r FileRetry) transient(err error) bool {
	if r.Transient != nil {
		return r.Transient(err)
	}
	return transientIO(err)
}

func (r FileRetry) ReadFile(path string) ([]byte, error) { return r.ReadFileBy(path, time.Time{}) }

func (r FileRetry) ReadFileBy(path string, budget time.Time) ([]byte, error) {
	deadline := steadyDeadline(budget)
	for {
		raw, err := readRegular(path)
		if err == nil || !r.transient(err) || !time.Now().Before(deadline) {
			return raw, err
		}
		time.Sleep(steadyPoll)
	}
}

func (r FileRetry) Rename(from, to string) error { return r.RenameBy(from, to, time.Time{}) }

func (r FileRetry) RenameBy(from, to string, budget time.Time) error {
	deadline := steadyDeadline(budget)
	for {
		err := os.Rename(from, to)
		if err == nil || !r.transient(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(steadyPoll)
	}
}

func readFileSteady(path string) ([]byte, error) { return (FileRetry{}).ReadFile(path) }

func readFileSteadyBy(path string, budget time.Time) ([]byte, error) {
	return (FileRetry{}).ReadFileBy(path, budget)
}

func renameSteady(from, to string) error { return (FileRetry{}).Rename(from, to) }

func renameSteadyBy(from, to string, budget time.Time) error {
	return (FileRetry{}).RenameBy(from, to, budget)
}

// missing is the one question `state` and its kin actually mean to ask of a failed read:
// is this record GONE, or was it merely unreadable for an instant? The second is not a
// fact about a job, and nothing in this package may finalize one on it.
func missing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

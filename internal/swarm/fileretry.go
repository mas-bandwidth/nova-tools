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

// THE WINDOW IS A CEILING, NEVER AN ADDITION TO SOMEBODY ELSE'S CLOCK.
//
// SteadyWindow is what ONE collision may cost. A caller that retries -- the launch
// handshake reads a slot file every 20ms for its whole launch timeout -- would otherwise
// pay it once per turn: 500 reads x 2s is 1010s spent inside a 10s bound, with run.lock
// held the entire time. So every caller that has a bound of its own hands it down, and the
// wait here ends at min(its own window, the caller's remaining budget). A caller with no
// bound passes the zero time and gets the window, as before.
// The steady waits take their time from a steadyClock: the wall clock in the binary, and a
// clock a test steps, so a wait out to a deadline costs the test no time. The deadline is
// computed from the now handed in.
func steadyDeadline(now, budget time.Time) time.Time {
	own := now.Add(SteadyWindow)
	if budget.IsZero() || own.Before(budget) {
		return own
	}
	return budget
}

// steadyClock is what a steady wait reads the time from, spends it on, and asks whether a
// failure is transient.
type steadyClock struct {
	now   func() time.Time
	sleep func(time.Duration)
	// transient is the SEAM for the one thing a unix test cannot produce: a read that
	// collides. Nil is the platform's rule (transientIO), the production answer in every
	// build; a test carries its own on the value under test -- the per-test seam the
	// serial-tests ledger names ("a field on the value under test"), so the bound above can
	// be proved on the machine that runs the tests rather than only on Windows, and no
	// package variable races the parallel tests beside it.
	transient func(error) bool
}

// isTransient is the wait's answer on a failed read: the clock's own seam, else the
// platform's rule.
func (c steadyClock) isTransient(err error) bool {
	if c.transient != nil {
		return c.transient(err)
	}
	return transientIO(err)
}

// readFileSteady reads a whole file, waiting out a transient collision with a concurrent
// atomic replace of the same path. A file that is NOT THERE is an answer, not a collision:
// it returns immediately, so `os.IsNotExist` callers keep the meaning they had.
//
// It reads through readRegular (regular.go), so a path that is a SYMLINK or a FIFO is an
// answer too -- refused at once, never followed and never waited on. That is every
// dispatcher read of a worker-writable record: this function, and ReadJSON through it.
func readFileSteady(path string) ([]byte, error) {
	return readFileSteadyBy(wallClock, path, time.Time{})
}

// wallClock is the real one, the only one outside a test.
var wallClock = steadyClock{now: time.Now, sleep: time.Sleep}

// readFileSteadyBy is readFileSteady under a caller's deadline: the collision wait gets the
// smaller of this package's window and what the caller has left.
func readFileSteadyBy(c steadyClock, path string, budget time.Time) ([]byte, error) {
	deadline := steadyDeadline(c.now(), budget)
	for {
		raw, err := readRegular(path)
		if err == nil || !c.isTransient(err) || !c.now().Before(deadline) {
			return raw, err
		}
		c.sleep(steadyPoll)
	}
}

// renameSteady renames, waiting out a reader that has the destination open. The caller's
// error is the LAST one, so a path that is genuinely wrong still reports what is wrong
// with it.
func renameSteady(from, to string) error { return renameSteadyBy(wallClock, from, to, time.Time{}) }

// renameSteadyBy is renameSteady under a caller's deadline, on the same terms as
// readFileSteadyBy.
func renameSteadyBy(c steadyClock, from, to string, budget time.Time) error {
	deadline := steadyDeadline(c.now(), budget)
	for {
		err := os.Rename(from, to)
		if err == nil || !c.isTransient(err) || !c.now().Before(deadline) {
			return err
		}
		c.sleep(steadyPoll)
	}
}

// missing is the one question `state` and its kin actually mean to ask of a failed read:
// is this record GONE, or was it merely unreadable for an instant? The second is not a
// fact about a job, and nothing in this package may finalize one on it.
func missing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

package wake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Work list item 3a: the --advance-cursor guard.
//
// A cursor is a claim about what a reader HAS BEEN SHOWN, and a watcher that
// advances a reader's cursor is making that claim on the reader's behalf. On
// 2026-09-11 a test run of the watcher read the window's bus WITH --advance,
// consumed five notes, and reported into a transcript nobody read: five notes
// on no open list, in no transcript, and the window had to be told by a person.
//
// So advancing is off by default, is permitted only for the window's own --as,
// is one advancing watcher per (bus, as), and is a TRANSACTION whose every step
// is ordered so that a kill leaves a repeated wake and never a lost note:
//
//	1. `inbox` WITHOUT --advance; every unprinted NOTE is spooled and the state
//	   written before anything is printed (rule 11, step 1).
//	2. print up to the cap; if any bus record is still queued after the print,
//	   THIS POLL DOES NOT ADVANCE.
//	3. behind an empty bus queue: write bus:advance=inflight|<stamp>|<head>,
//	   run `inbox --advance --remote --branch`, spool what it listed, write the
//	   state, and only then clear the marker.
//	4. a call that finds the marker runs `inbox` plainly, reads carrying=<n>,
//	   and lists the whole carried list with `inbox --open --open-max <n>` --
//	   n, never a fixed number, because nova-bus caps the listed OPEN at
//	   --open-max and a fixed number would recover a fixed number.
//
// nova-bus moves a cursor to HEAD and nowhere else, so this is the only way to
// keep the cursor behind the print.

// AdvanceMarker is the state key that says an advance was in flight. It is
// written BEFORE the advance and cleared AFTER its output is durable, so a kill
// between the two leaves the marker for the next call to recover through.
const AdvanceMarker = "bus:advance"

// AdvanceKillPoint is the injected kill of test 11: the residual race is a kill
// between `nova-bus inbox --advance` RETURNING and the write of its output, and
// a test cannot SIGKILL a function it is calling. It is never set outside a
// test.
var AdvanceKillPoint string

// Advancer is the transaction. It holds no state of its own beyond the bus
// source it advances through and the clock that stamps the marker.
type Advancer struct {
	Bus   *Bus
	Clock Clock
}

// LockAdvance takes the exclusive lock named by (bus, as) for the whole call --
// not per poll, unlike nova-bus wait, because the thing being protected is a
// cursor this run is MOVING rather than a checkout it is reading. A second
// watcher over the same pair is refused, naming the holder.
//
// The lock lives beside the bus it names, which is a directory the caller gave:
// nothing here guesses a path, and nothing here writes to /tmp (rule 4).
func LockAdvance(bus, as string) (release func(), holder string, err error) {
	return lockAt(filepath.Join(bus, ".nova-wake-advance-"+safeName(as)+".lock"))
}

// safeName keeps a name that reaches the filesystem to one path segment.
func safeName(as string) string {
	out := make([]rune, 0, len(as))
	for _, r := range as {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return string(out)
}

// Interrupted reports whether an advance was in flight when the last call died.
func Interrupted(st *State) bool {
	v, ok := st.Get(AdvanceMarker)
	return ok && strings.HasPrefix(v, "inflight")
}

// Recover is step 4. It runs at the START of a call that finds the marker, and
// before anything else is polled: a plain `inbox` for carrying=, then the whole
// carried list, spooled. When the --open read lists FEWER than carrying= -- the
// checkout moved between the two reads -- it is run once more with the new
// count; still short, the marker stays, nothing advances, and the call says so.
func (a *Advancer) Recover(ctx context.Context, st *State) (Result, string, error) {
	var res Result
	out, code, err := a.Bus.run(ctx, a.Bus.inboxArgs()...)
	if err != nil {
		return res, "", err
	}
	if code != 0 {
		return res, "", fmt.Errorf("nova-bus exit=%d", code)
	}
	n := carrying(out)
	if n <= 0 {
		st.Delete(AdvanceMarker)
		return res, "bus advance was interrupted; recovered 0 notes from OPEN", nil
	}
	listed, res, err := a.open(ctx, n, &res)
	if err != nil {
		return res, "", err
	}
	if listed < n {
		// The checkout moved between the two reads. Once more, with the count
		// this read saw.
		again := carrying(out)
		if listed > 0 {
			again = listed
		}
		listed, res, err = a.open(ctx, again, &res)
		if err != nil {
			return res, "", err
		}
		if listed < n {
			return res, fmt.Sprintf("bus recovery incomplete: listed=%d carrying=%d; retried next call", listed, n), nil
		}
	}
	st.Delete(AdvanceMarker)
	return res, fmt.Sprintf("bus advance was interrupted; recovered %d notes from OPEN", listed), nil
}

// open runs `inbox --open --open-max <n>` once and classifies what it listed.
func (a *Advancer) open(ctx context.Context, n int, res *Result) (int, Result, error) {
	before := len(res.Items)
	out, code, err := a.Bus.run(ctx, append(a.Bus.inboxArgs(), "--open", "--open-max", strconv.Itoa(n))...)
	if err != nil {
		return 0, *res, err
	}
	if code != 0 {
		return 0, *res, fmt.Errorf("nova-bus exit=%d", code)
	}
	a.Bus.classify(out, res)
	listed := len(res.Items) - before
	// A note this tool has already printed is suppressed by the classifier and
	// is still a note the bus listed: the count that matters to the re-read is
	// what the OPEN list held.
	if n := countNotes(out); n > listed {
		listed = n
	}
	return listed, *res, nil
}

func countNotes(out string) int { return len(BusNoteIDs(out)) }

// Advance is step 3, and it is called ONLY behind an empty bus queue. The
// marker is written before the advance and cleared after its output is durable.
func (a *Advancer) Advance(ctx context.Context, st *State, head string, save func()) (res Result, killed bool, err error) {
	st.Set(AdvanceMarker, Compose("inflight", Stamp(a.Clock.Now()), head))
	save()
	args := append(a.Bus.inboxArgs(), "--advance", "--remote", a.Bus.Remote, "--branch", a.Bus.Branch)
	out, code, err := a.Bus.run(ctx, args...)
	if AdvanceKillPoint == "after-advance" {
		// The residual race, named in The races and closed by the recovery
		// rather than by a refusal: a kill HERE loses this tool's record of
		// anything that read listed first, and a plain inbox does not re-list a
		// note the cursor has passed. The marker is what brings them back.
		return res, true, nil
	}
	a.Bus.classify(out, &res)
	if err != nil {
		return res, false, err
	}
	if code != 0 {
		return res, false, fmt.Errorf("nova-bus exit=%d", code)
	}
	return res, false, nil
}

// ClearAdvance is the last step of the transaction: the marker is cleared only
// after the advance's output has been written to the state.
func ClearAdvance(st *State) { st.Delete(AdvanceMarker) }

// BusQueued reports whether any queue record names a bus note. The cursor never
// moves while one does: mail consumed is mail spooled, mail spooled is mail
// printed, and a cap that elides a note defers the fetch rather than losing it.
func BusQueued(st *State) int {
	n := 0
	for _, r := range st.Queue() {
		if strings.HasPrefix(r.Key, "bus:note:") {
			n++
		}
	}
	return n
}

// lockAt is LockState's body over any path, so that the (bus, as) lock and the
// state lock are one implementation and not two.
func lockAt(lock string) (release func(), holder string, err error) {
	f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("the lock that keeps two advancing watchers off one cursor could not be opened at %s: %w", lock, err)
	}
	ok, lockErr := tryLockFile(f)
	if lockErr != nil {
		f.Close()
		return nil, "", fmt.Errorf("the lock at %s could not be taken: %w", lock, lockErr)
	}
	if !ok {
		f.Close()
		return nil, readHolder(lock), nil
	}
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
		f.Sync()
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		unlockFile(f)
		f.Close()
	}, "", nil
}

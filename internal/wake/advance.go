package wake

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
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
func LockAdvance(busDir, as string) (release func(), holder string, err error) {
	// NOT IN THE WORKTREE. `nova-bus inbox --advance` -- the one write-side call
	// this tool makes -- refuses a checkout that holds changes that are not the
	// note it is writing, and an untracked dotfile is such a change: a lock at
	// the bus root makes every advance fail, so the watcher never fetches and
	// looks perfectly healthy while it is blind. internal/bus puts nova-bus's
	// own lock in the git directory for the neighbouring reason ("a lock at the
	// bus root would be a file on the bus that every reader would then have to
	// know is not a note"), and the git directory is per-CHECKOUT, which is the
	// scope (bus, as) means. A bus that is not a git checkout has no git
	// directory and no worktree to dirty, so the lock sits beside it there.
	dir := busDir
	if gd, gerr := bus.GitDir(busDir); gerr == nil && gd != "" {
		dir = gd
	}
	lock := filepath.Join(dir, "nova-wake-advance-"+safeName(as)+".lock")
	// internal/bus's LockFile, the ONE lock implementation in this repo since
	// Emma exported it: an flock the kernel drops when the process dies, an
	// O_EXCL sentinel where there is no flock. A second advancing watcher does
	// not wait -- a watch runs for its whole --max, and waiting out a
	// twenty-minute holder is not a refusal anybody wants.
	rel, err := bus.LockFile(lock, 0)
	if err != nil {
		if errors.Is(err, bus.ErrLockHeld) {
			return nil, bus.ReadLockHolder(lock), nil
		}
		return nil, "", fmt.Errorf("the lock that keeps two watchers off one cursor could not be taken at %s: %w", lock, err)
	}
	return rel, "", nil
}

// safeName keeps a name that reaches the filesystem to one path segment, and it
// ENCODES rather than strips: the lock is named by (bus, as), so two different
// names that share a lock refuse each other over a cursor neither of them is
// moving. Every byte outside [A-Za-z0-9_-] becomes %<hex>, and % itself is
// encoded first, so the mapping is injective and reversible by eye.
func safeName(as string) string {
	var b strings.Builder
	for i := 0; i < len(as); i++ {
		c := as[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	if b.Len() == 0 {
		return "%00"
	}
	return b.String()
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
	// Rule 7 is absolute and covers this read too: every line the bus source
	// reads is classified as suppressed, relayed or standing, and every line is
	// counted -- BEFORE the exit code is looked at, because a nova-bus that
	// said NO said it in lines, and a recovery that returned on the code would
	// drop exactly the REFUSED line the rule was written for. The non-zero exit
	// is then a failed poll like any other, which is what reaches the streak.
	a.Bus.classify(out, &res)
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
		// The checkout moved between the two reads, so the count is READ AGAIN
		// rather than guessed from the short list: a short read of 0 would
		// otherwise ask the same question twice and call the answer incomplete,
		// and a short read of k would ask for k when the checkout may now carry
		// more than that.
		fresh, code, ferr := a.Bus.run(ctx, a.Bus.inboxArgs()...)
		if ferr != nil {
			return res, "", ferr
		}
		if code != 0 {
			return res, "", fmt.Errorf("nova-bus exit=%d", code)
		}
		a.Bus.classify(fresh, &res)
		again := carrying(fresh)
		if again <= 0 {
			// The checkout moved and the reader carries nothing now: there is
			// nothing left for this recovery to reach, and asking the same
			// question a second time with the count that is already stale is
			// the defect the re-read exists to remove.
			st.Delete(AdvanceMarker)
			return res, fmt.Sprintf("bus advance was interrupted; recovered %d notes from OPEN", listed), nil
		}
		listed, res, err = a.open(ctx, again, &res)
		if err != nil {
			return res, "", err
		}
		if listed < again {
			return res, fmt.Sprintf("bus recovery incomplete: listed=%d carrying=%d; retried next call", listed, again), nil
		}
	}
	st.Delete(AdvanceMarker)
	return res, fmt.Sprintf("bus advance was interrupted; recovered %d notes from OPEN", listed), nil
}

// open runs `inbox --open --open-max <n>` once and classifies what it listed.
func (a *Advancer) open(ctx context.Context, n int, res *Result) (int, Result, error) {
	before := len(res.Items)
	out, code, err := a.Bus.run(ctx, append(a.Bus.inboxArgs(), "--open", "--open-max", strconv.Itoa(n))...)
	// Classified first, for the reason above: nothing this tool reads from the
	// bus is dropped because of an exit code.
	a.Bus.classify(out, res)
	if err != nil {
		return 0, *res, err
	}
	if code != 0 {
		return 0, *res, fmt.Errorf("nova-bus exit=%d", code)
	}
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

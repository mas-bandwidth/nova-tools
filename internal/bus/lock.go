package bus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
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
// The lock is internal/filelock's, the one lock (its design and invariants are
// tla/FileLock.tla: the take and release actions and the HeldIsTrue invariant; the
// kernel releases the lock when its holder dies, so there is no stale sentinel to
// clear), on a file inside the git directory. Inside the git directory because that is
// per-CHECKOUT: two linked worktrees of one repository are two checkouts and must not
// block each other, and a lock at the bus root would be a file on the bus that every
// reader would then have to know is not a note.

// LockName is the lock file, in the checkout's git directory.
const LockName = "nova-bus.lock"

// LockCheckout takes this checkout's lock, waiting up to wait for it, and returns the
// release. The release is safe to call more than once.
//
// A bus that is not a git checkout at all has nothing to lock and is not locked: the only
// verbs that reach one are the full reads, which write nothing, and refusing them for the
// want of a `.git` would be refusing a bus for a reason that is not about the bus.
//
// The take is filelock.Lock on the same file at the same path every earlier binary of this
// tool locked, so an old and a new binary exclude each other across an upgrade. A fresh
// lock file is created here first, exclusively and 0644 as this tool always made it;
// O_EXCL refuses whatever already stands at the path, a symlink included, so nothing is
// ever created through a link.
func LockCheckout(busDir string, wait time.Duration) (func(), error) {
	gd, err := GitDir(busDir)
	if err != nil {
		return func() {}, nil
	}
	path := filepath.Join(gd, LockName)
	fresh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		// ignored: an empty file closed at once; filelock reopens it, and its open reports any fault
		_ = fresh.Close()
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("the lock that keeps two runs off one checkout could not be opened at %s: %w", path, err)
	}
	l, err := filelock.Lock(path, "nova-bus", wait)
	if errors.Is(err, filelock.ErrHeld) || errors.Is(err, filelock.ErrBusy) {
		return nil, fmt.Errorf("another nova-bus is already running on this checkout and still holds %s; this run waited %s for it and will not work beside it, because two runs on one checkout write one OPEN list and one index -- run this again when that one has finished", path, wait)
	}
	if err != nil {
		return nil, fmt.Errorf("the lock that keeps two runs off one checkout could not be opened at %s: %w", path, err)
	}
	// ignored: release has no caller to report to; the kernel lock goes with the descriptor either way
	return func() { _ = l.Unlock() }, nil
}

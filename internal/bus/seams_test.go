package bus

// The thin wrappers only tests call: each names a fixed argument of a function the tools
// reach (CheckWith, PrepareWith, PrepareReplyFrom, clearStaleIndexLockReport,
// gitOwnsCheckoutScan), so a test reads as the case it pins. They live with the tests so
// that nothing a shipped tool cannot reach is in the package.

import (
	"os"
	"time"
)

// clearStaleIndexLock is ClearStaleIndexLock with the process scan supplied.
// A test hands back an incomplete scan — a cwd git with no -C whose cwd could
// not be read, or a permission error — and the lock must still be here afterwards.
func clearStaleIndexLock(dir string, now time.Time, scan func() ([]gitProc, error)) (bool, error) {
	return clearStaleIndexLockAs(dir, now, scan, indexLockOwner, effectiveUID())
}

// clearStaleIndexLockAs is clearStaleIndexLock with the lock file's owner reader and this
// account's uid supplied. The age rule comes first; the owner of a stale lock is read
// from the Lstat of the lock itself, and only a stale lock owned by self goes on to the
// process scan.
func clearStaleIndexLockAs(dir string, now time.Time, scan func() ([]gitProc, error), lockOwner func(os.FileInfo) (uint32, bool), self uint32) (bool, error) {
	rep, err := clearStaleIndexLockReport(dir, now, scan, lockOwner, self)
	return rep.Cleared, err
}

// gitOwnsCheckout reports whether a live git process is operating on dir. An error means
// the question could not be answered, which the caller treats as "do not remove the lock".
func gitOwnsCheckout(dir string) (bool, error) {
	found, err := gitOwnsCheckoutScan(dir, gitProcesses)
	return found.Owner > 0, err
}

// PrepareReply builds the note that answers an existing one, with every header the reply
// verb fills taken from the original and NONE from the caller: From is the caller, To is
// the original's From, Re names the original by id (or path, for a note older than ids),
// and Subject is the original's subject with one `Re: ` in front, not stacked. It is the
// step that makes a reply impossible to hand-shape: a caller supplies the body and nothing
// else, so there is no header line to get wrong.
func PrepareReply(t *Bus, me Participant, original *Note, body string, now time.Time) (Prepared, error) {
	return PrepareReplyFrom(t, me, original, body, now, "")
}

// Prepare validates a draft, assigns its id and date, and works out where it goes. It
// writes nothing: every refusal here happens before the bus is touched.
//
// It is PrepareDraft with nobody named by --as, which is what a caller with a draft that
// already carries its own From line has.
func Prepare(t *Bus, text string, now time.Time, slugOverride string) (Prepared, error) {
	return PrepareWith(t, text, now, SendOptions{Slug: slugOverride})
}

// Check validates the whole bus and returns every problem it found, sorted. This is what
// CI on a bus runs, so it names every failure in one pass rather than the first.
//
// What it asserts: every note parses; every header is valid against the roster; every note
// sits in the lane its From line names; every id is well formed, carries its own lane's
// slug, and is unique across the bus; every Re resolves to an id or to a path that
// exists; every receipt line parses and names something that exists; every from-* lane on
// disk has an owner in the roster; and a lane holds notes and its RECEIPTS file and
// nothing else.
//
// What it deliberately does not assert: anything about a note's body. A body is prose,
// and prose is the part of the bus no tool has an opinion about.
//
// Its findings about a note's HEADER can be TOLERATED for old notes rather than failed;
// see CheckOptions. Check itself tolerates nothing.
func (t *Bus) Check() []Problem { return t.CheckWith(CheckOptions{}) }

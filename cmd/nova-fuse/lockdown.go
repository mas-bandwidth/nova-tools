// lockdown.go holds the lockdown verb: its flags, its run and the helpers only it uses.

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdLockdown stops everything. It is the one command that must work even when the fuse
// box is already broken: a fuse you cannot blow is not a fuse.
func cmdLockdown(rest []string, stdout, stderr io.Writer, now time.Time, inv invocation) int {
	box, positional, ok, parsed, dry := parseWrite("lockdown", rest, stderr, inv.getenv)
	if !parsed {
		return 2
	}
	// Joined, not positional[0]: an unquoted `lockdown suspected compromise` records
	// "suspected compromise" rather than dropping everything after the first word, which
	// would lose the audit trail of the most serious action this tool can take.
	// Folded, never refused: a reason carrying a newline is still a reason, and a fuse you
	// cannot blow is not a fuse. Folding only tidies what this tool writes; the guarantee
	// that an event stays one line is made at print time, because the box is hand-editable
	// and the next reason may not have come from here at all.
	reason := keepableReason(strings.Join(positional, " "))
	if reason == "" {
		// Printed even when --box was missing too: one run, every problem.
		refuse(stderr, " lockdown", "needs a reason: `lockdown --box <path> \"<reason>\"`")
		ok = false
	}
	if !ok {
		return 2
	}

	// One cross-process read-modify-write: the read, the blow and the write all
	// happen under the box's lock, so a quarantine or a lift running at the same
	// time reads this lockdown before it publishes and the lockdown survives it
	// (tla/FuseBox.tla, LockdownMonotone; fuse.MutateBox). Before this, a soft
	// writer holding a read of a clear box published its older snapshot over a
	// lockdown blown and verified in between, and both runs reported success. A
	// dry run takes no lock and writes nothing.
	var standing *fuse.Fuse
	what := "blow the lockdown in the box there"
	err := fuse.MutateBox(boxFile(inv.wd, box), !dry, func(b fuse.Box, readErr error) (fuse.Box, bool, error) {
		switch {
		case dry && errors.Is(readErr, fuse.ErrNoBox):
			what = "make a box there holding a blown lockdown"
			b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
		case dry && readErr != nil:
			what = "keep the unreadable box's bytes beside it and replace it with a blown lockdown"
			b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
		case dry:
		case errors.Is(readErr, fuse.ErrNoBox):
			// A fuse you cannot blow is not a fuse: with no box there, the lockdown makes
			// one. Nothing is less blocked than before, since no box already refused.
			b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
		case readErr != nil:
			// Proceed anyway, and this direction is safe to argue precisely: before, an
			// unreadable box made every caller refuse; after, a recorded lockdown makes every
			// caller refuse. Nothing is less blocked than it was, and the box becomes readable
			// again. The refusing direction is not symmetric: cmdQuarantine refuses for the
			// mirror-image reason.
			b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
			dst, perr := fuse.PreserveUnreadable(boxFile(inv.wd, box))
			if perr != nil {
				fmt.Fprintf(stderr, "LOCKDOWN NOTE box was unreadable (%s) and its bytes could NOT be preserved (%s); blowing lockdown anyway\n", oneline.Err(readErr), oneline.Err(perr))
			} else {
				fmt.Fprintf(stderr, "LOCKDOWN NOTE box was unreadable (%s); its bytes are kept at %s -- any quarantine it recorded is NOT carried forward, and lockdown blocks everything, so nothing is less blocked than before\n", oneline.Err(readErr), oneline.Escape(dst))
			}
		}

		// A lockdown already standing is the state this verb seeks, and the first time
		// and why of the blow are audit facts, unrecoverable once overwritten
		// (security#74 finding 1): the box is kept as it stands, never rewritten,
		// and the run says so. FuseBox.tla's Lockdown(b) over an already-blown box
		// leaves lock TRUE and keeps the quarantines -- idempotent -- and this is
		// that action at the box's finest grain, the stamp and the reason. The exit
		// stays 0 because the state sought holds. A dry run prints the same line: by
		// not writing it keeps the record too.
		if s := b.Lockdown; s != nil {
			standing = s
			return b, false, nil
		}

		b.Lockdown = &fuse.Fuse{At: stamp(now), Reason: reason}
		return b, true, nil
	})
	if standing != nil {
		fmt.Fprintf(stdout, "LOCKDOWN OK already=blown since=%s: %s (standing record kept; the new reason was not recorded: %s)\n",
			since(*standing), why(*standing), oneline.Escape(reason))
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "LOCKDOWN FAILED could not write box: %s (the write is temp-file + rename, so a failure cannot leave it torn; stop by hand and tell the person you work with now)\n", oneline.Err(err))
		return 1
	}
	if dry {
		fmt.Fprintf(stdout, "LOCKDOWN OK dry_run=true: nothing written, the lockdown is not blown; a real run would %s: %s\n", oneline.Escape(what), oneline.Escape(reason))
		return 0
	}

	// The exit code of a remedy is not evidence the remedy worked; the state afterwards
	// is. Re-read, always.
	after, err := fuse.ReadBox(boxFile(inv.wd, box))
	if err != nil || after.Lockdown == nil {
		fmt.Fprintf(stderr, "LOCKDOWN FAILED written but unverifiable (%s): do not trust it; stop by hand and tell the person you work with now\n", oneline.Err(err))
		return 1
	}

	fmt.Fprintf(stdout, "LOCKDOWN OK since=%s: %s (verified by re-reading the box; all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with -- go have it now)\n",
		oneline.Field(after.Lockdown.At), oneline.Escape(reason))
	return 0
}

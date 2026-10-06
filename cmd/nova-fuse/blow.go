// blow.go holds the verbs that blow a fuse, lockdown and quarantine, and the helpers only they use: blow the hard fuse and verify it; stop reading one surface and verify it.

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
	wantsBlow := false
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
		wantsBlow = true
		return b, true, nil
	})
	if standing != nil {
		fmt.Fprintf(stdout, "LOCKDOWN OK already=blown since=%s: %s (standing record kept; the new reason was not recorded: %s)\n",
			since(*standing), why(*standing), oneline.Escape(reason))
		return 0
	}
	// The box's lock refused to be taken -- another writer holds it, the wait timed
	// out -- and this is the one mutation that answers that by blowing anyway: a
	// fuse you cannot blow is not a fuse (fuse.BoxLockUntaken, note 3). WriteBox
	// publishes by temp-file + rename, so the blow is still atomic -- what it loses
	// is the serialization, and that loss is said on the NOTE line rather than
	// kept silent under an exit 0.
	if err != nil && wantsBlow && fuse.BoxLockUntaken(err) {
		fmt.Fprintf(stderr, "LOCKDOWN NOTE could not take the box's lock (%s): blowing the lockdown anyway, unserialized -- a quarantine landing at the same moment may still be overwritten by this one, or this one by it\n", oneline.Err(err))
		next, rerr := fuse.ReadBox(boxFile(inv.wd, box))
		if rerr != nil {
			// The same reading the closure gives an unreadable box: a fresh one
			// holding the lockdown, and nothing is less blocked than before.
			next = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
		}
		next.Lockdown = &fuse.Fuse{At: stamp(now), Reason: reason}
		err = fuse.WriteBox(boxFile(inv.wd, box), next)
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

// cmdQuarantine stops ONE surface.
func cmdQuarantine(rest []string, stdout, stderr io.Writer, now time.Time, inv invocation) int {
	box, positional, ok, parsed, dry := parseWrite("quarantine", rest, stderr, inv.getenv)
	if !parsed {
		return 2
	}
	surface, reason := "", ""
	if len(positional) >= 2 {
		surface = fuse.Surface(positional[0])
		reason = keepableReason(strings.Join(positional[1:], " ")) // folded, never refused -- see cmdLockdown
	}
	if surface == "" || reason == "" {
		// Printed even when --box was missing too: one run, every problem.
		refuse(stderr, " quarantine", "needs a surface and a reason: `quarantine --box <path> <surface> \"<reason>\"`")
		ok = false
	}
	if !ok {
		return 2
	}

	// One cross-process read-modify-write: the read, the entry and the write all
	// happen under the box's lock, so this run never publishes a snapshot older
	// than a lockdown blown while it held its read (tla/FuseBox.tla,
	// LockdownMonotone; fuse.MutateBox), and two quarantines of one box both land
	// instead of one erasing the other. A dry run takes no lock and writes nothing.
	var (
		readErr     error
		standingKey string
		standing    fuse.Fuse
	)
	err := fuse.MutateBox(boxFile(inv.wd, box), !dry, func(b fuse.Box, rerr error) (fuse.Box, bool, error) {
		readErr = rerr
		if rerr != nil {
			return b, false, nil
		}
		// The same rule as lockdown's, per surface: a quarantine already standing on
		// this surface -- under any fold-equivalent spelling, the way Quarantined
		// matches -- is the state sought, so the first time and why are kept and the
		// run says so instead of rewriting them (security#74 finding 1). FuseBox.tla's
		// Quarantine(b, s) unions {s} into a set that may already hold it -- idempotent
		// -- and this is that action at the entry's finest grain, the stamp and the
		// reason. The line names the STORED spelling, the one entry whose at and
		// reason it quotes. The exit stays 0; the state sought holds. A dry run
		// prints the same line: by not writing it keeps the record too.
		if name, s, ok := b.Quarantined(string(surface)); ok {
			standingKey, standing = name, s
			return b, false, nil
		}
		b.Quarantine[surface] = fuse.Fuse{At: stamp(now), Reason: reason}
		return b, true, nil
	})
	if errors.Is(readErr, fuse.ErrNoBox) {
		// Refuse, for the same reason as an unreadable box: with no box there every
		// surface is refused, and a new box holding only this quarantine would clear
		// the rest.
		fmt.Fprintf(stderr, "nova-fuse quarantine REFUSED: %s -- refusing to make a box holding only this quarantine: with no box every surface is refused, and that box would clear the rest; make the box first (%s), or blow lockdown; run: nova-fuse help\n", oneline.Err(readErr), oneline.Escape(boxRemedy("init", box)))
		return 2
	}
	if readErr != nil {
		// Refuse: this is the asymmetry with lockdown, not an inconsistency with it. An
		// unreadable box blocks every surface; replacing it with a fresh box holding only
		// this one quarantine would unblock everything else, so the safety-shaped action
		// would fail open. Under doubt, no.
		fmt.Fprintf(stderr, "nova-fuse quarantine REFUSED: %s -- refusing to narrow an unreadable box: while unreadable it already blocks EVERY surface, and a fresh box holding only this one quarantine would UNBLOCK the rest; blow lockdown instead (`lockdown --box %s \"<reason>\"`), or repair the box by hand with the person you work with; run: nova-fuse help\n", oneline.Err(readErr), oneline.Escape(box))
		return 2
	}
	if standingKey != "" {
		fmt.Fprintf(stdout, "QUARANTINE OK %s already=quarantined since=%s: %s (standing record kept; the new reason was not recorded: %s)\n",
			oneline.Field(standingKey), since(standing), why(standing), oneline.Escape(reason))
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "QUARANTINE FAILED %s: could not write box: %s (the box was not replaced; stop reading that surface by hand and tell the person you work with)\n", oneline.Field(surface), oneline.Err(err))
		return 1
	}
	if dry {
		fmt.Fprintf(stdout, "QUARANTINE OK %s dry_run=true: nothing written, the surface is not quarantined; a real run would record: %s\n", oneline.Field(surface), oneline.Escape(reason))
		return 0
	}

	// Re-read. The exit code of a remedy is not evidence the remedy worked. And ask for the
	// key that was written, not for any entry folding to it: a box that already held another
	// spelling of this surface would otherwise verify through the sorted-first sibling and
	// announce its name and its old stamp under the new reason, a true claim about the wrong
	// entry. Quarantined is the right question for the gate; here the question is whether
	// this write landed.
	after, err := fuse.ReadBox(boxFile(inv.wd, box))
	landed, ok2 := after.Quarantine[surface]
	if err != nil || !ok2 {
		fmt.Fprintf(stderr, "QUARANTINE FAILED %s: written but unverifiable (%s): do not trust it; stop reading that surface by hand and tell the person you work with\n", oneline.Field(surface), oneline.Err(err))
		return 1
	}

	fmt.Fprintf(stdout, "QUARANTINE OK %s since=%s: %s (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)\n",
		oneline.Field(surface), since(landed), oneline.Escape(reason))
	return 0
}

// stamp formats the injected clock. RFC3339 in UTC, exact to the second, so no wall clock
// and no float ever enters a recorded fact.
func stamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// keepableReason is how a reason is stored, and it exists to make sure folding never
// becomes a refusal. Fold turns control characters into spaces, so a reason made of
// nothing but them folds away to nothing -- and refusing that would mean a fuse that
// could be blown yesterday cannot be blown today, which is the one direction this design
// forbids. A reason made entirely of NON-WHITESPACE control characters is therefore kept
// as its visible escapes instead, so the record says what was typed. The whitespace half
// of that category is not a change and is not treated as one: a reason of nothing but
// newlines, tabs, CR, VT, FF or U+0085 trims to empty and is refused, which is what it
// did before this branch too. A genuinely empty or all-whitespace reason still comes back
// empty here, and the caller still refuses it, exactly as before.
func keepableReason(raw string) string {
	if folded := fuse.Fold(raw); folded != "" {
		return folded
	}
	return oneline.Escape(strings.TrimSpace(raw))
}

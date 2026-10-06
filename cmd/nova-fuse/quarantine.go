// quarantine.go holds the quarantine verb: its flags, its run and the helpers only it uses.

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

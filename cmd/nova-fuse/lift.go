// lift.go holds the lift verb and the helpers only it uses: rescind your own quarantine, announced and verified, and refuse lift lockdown by design.

package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdLift is the fuse design made code, and the asymmetry runs between the powers.
// Quarantine is soft: your own decision, in both directions, so rescinding one succeeds,
// out loud, verified, never silently. Lockdown is hard: the refusal below is answered
// before any flag is parsed, takes no argument into account, and names the only remedy
// there is, a conversation.
//
// The one refusal in this file that is not one line is `lift lockdown`, and it is meant
// to be read rather than scanned.
func cmdLift(rest []string, stdout, stderr io.Writer, inv invocation) int {
	if len(rest) == 0 {
		return refuse(stderr, " lift", "takes a power first: `lift quarantine --box <path> <surface>` -- and `lift lockdown` is refused by design")
	}
	switch rest[0] {
	case "lockdown":
		// Always, and before anything is read. The refusal must not depend on a flag being
		// parsed, the box being readable, whether a lockdown is even blown, or anything
		// after the word "lockdown", because every one of those is a lever. There is no
		// code path from here to clearing a lockdown, no override flag and no environment
		// variable, and the remedy named is a conversation, not a mechanism.
		fmt.Fprint(stderr, "nova-fuse lift lockdown REFUSED, forever, by design: a blown fuse is not reset;\n"+
			"it is REPLACED, and only in a live conversation with the person you work with.\n"+
			"Nothing this tool is told changes that. Stop, and go talk with them now.\n")
		return 2
	case "quarantine":
		box, positional, ok, parsed, dry := parseWrite("lift quarantine", rest[1:], stderr, inv.getenv)
		if !parsed {
			return 2
		}
		if len(positional) != 1 || fuse.Surface(positional[0]) == "" {
			// Printed even when --box was missing too: the two are independent,
			// and one run should name both.
			refuse(stderr, " lift quarantine", "needs exactly one surface: `lift quarantine --box <path> <surface>`")
			ok = false
		}
		if !ok {
			return 2
		}
		return liftQuarantine(box, positional[0], dry, stdout, stderr, inv.wd)
	}
	return refuse(stderr, " lift", fmt.Sprintf("does not know %q -- lift takes a power first: `lift quarantine --box <path> <surface>` (`lift lockdown` is refused by design)", rest[0]))
}

// liftQuarantine rescinds one quarantine: the soft dial, turned the other way. Same
// discipline as blowing one: the write is verified by re-reading the box, and the
// rescind is announced, because a quarantine that vanishes silently is a decision nobody
// can audit. A dry run makes every check and writes nothing.
func liftQuarantine(box, surface string, dry bool, stdout, stderr io.Writer, wd string) int {
	// One cross-process read-modify-write: the read, the lift and the write all
	// happen under the box's lock, so a lockdown blown while this run holds its
	// read is in the box this run publishes (tla/FuseBox.tla, LockdownMonotone;
	// fuse.MutateBox). A dry run takes no lock and writes nothing.
	var (
		removed map[string]fuse.Fuse
		readErr error
		listed  string
	)
	err := fuse.MutateBox(boxFile(wd, box), !dry, func(b fuse.Box, rerr error) (fuse.Box, bool, error) {
		readErr = rerr
		if rerr != nil {
			return b, false, nil
		}
		removed = b.LiftQuarantine(surface)
		if len(removed) == 0 {
			// A typo must never read as a lift: name what is quarantined so the
			// mismatch is visible at a glance.
			listed = "none"
			if names := b.Surfaces(); len(names) > 0 {
				shown := make([]string, 0, len(names))
				for _, n := range names {
					shown = append(shown, oneline.Escape(n))
				}
				listed = strings.Join(shown, ", ")
			}
			return b, false, nil
		}
		return b, true, nil
	})
	if readErr != nil {
		// Refuse, the mirror of quarantine's refusal to narrow: while the box is
		// unreadable every fuse is treated as blown, and nothing provable can be lifted
		// from a box that cannot be read. The corrupt bytes stay put: they are evidence.
		fmt.Fprintf(stderr, "nova-fuse lift quarantine REFUSED: %s -- while the box cannot be read every fuse is treated as BLOWN; nothing provable can be lifted from it; %s; run: nova-fuse help\n", oneline.Err(readErr), oneline.Escape(remedy(readErr, box)))
		return 2
	}
	if len(removed) == 0 {
		fmt.Fprintf(stderr, "LIFT FAILED quarantine=%s: nothing to lift; not quarantined (quarantined now: %s)\n",
			oneline.Field(fuse.Surface(surface)), listed)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "LIFT FAILED quarantine=%s: could not write box: %s (the box was not replaced, so the quarantine still stands)\n",
			oneline.Field(fuse.Surface(surface)), oneline.Err(err))
		return 1
	}
	if dry {
		for _, n := range slices.Sorted(maps.Keys(removed)) {
			fmt.Fprintf(stdout, "LIFT OK quarantine=%s dry_run=true: would lift it; nothing written, it still stands\n", oneline.Field(n))
		}
		return 0
	}

	// Re-read. The exit code of a remedy is not evidence the remedy worked.
	after, err := fuse.ReadBox(boxFile(wd, box))
	if err != nil {
		fmt.Fprintf(stderr, "LIFT FAILED quarantine=%s: written but unverifiable: %s (do not trust it; treat the surface as still quarantined and tell the person you work with)\n",
			oneline.Field(fuse.Surface(surface)), oneline.Err(err))
		return 1
	}
	if name, _, still := after.Quarantined(surface); still {
		fmt.Fprintf(stderr, "LIFT FAILED quarantine=%s: lift did not take; %s is still quarantined on re-read (do not trust this run; tell the person you work with)\n",
			oneline.Field(fuse.Surface(surface)), oneline.Escape(name))
		return 1
	}

	// Announce under the stored spellings, sorted so two runs print the same bytes.
	for _, n := range slices.Sorted(maps.Keys(removed)) {
		f := removed[n]
		fmt.Fprintf(stdout, "LIFT OK quarantine=%s was since=%s: %s\n", oneline.Field(n), since(f), why(f))
	}
	fmt.Fprintf(stdout, "LIFT OK verified: %s is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)\n",
		oneline.Escape(fuse.Surface(surface)))
	if after.Lockdown != nil {
		fmt.Fprintf(stderr, "LIFT NOTE lockdown is still blown (since=%s) and blocks everything regardless\n",
			since(*after.Lockdown))
	}
	return 0
}

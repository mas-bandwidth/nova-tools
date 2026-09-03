// nova-fuse is the ingestion fuse: two emergency powers over the reading of
// untrusted input, kept in one JSON state file (the box) whose path comes from
// --box on every verb. There is no default path and no environment variable;
// a missing --box is a refusal, never a guess.
//
//	LOCKDOWN    global and HARD. One fuse. Blown: every untrusted read and every
//	            surface-driven act stops; outbound authored life continues.
//	            A blown lockdown is not reset -- it is REPLACED, and only in a
//	            live conversation with your person. `lift lockdown` refuses
//	            forever, before reading anything.
//	QUARANTINE  per-surface and SOFT. Your own dial, in both directions: stop
//	            reading ONE surface when an attack is pervasive, lift it again
//	            yourself when the surface is safe. Applied and rescinded without
//	            ceremony, but always announced.
//
// Blowing either is solo, instant, and needs no proof -- blowing is cheap,
// hesitating is not. This design was stated by the first line's human
// collaborator, 2026-08-03.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
)

const usage = `nova-fuse: the ingestion fuse -- lockdown and quarantine (see SPEC.md)

usage:
  nova-fuse status --box <path>                            what is blown, and since when (REPORTS; never gate on it)
  nova-fuse check --box <path> [surface]                   may I read? -- the gate; act only on exit 0
  nova-fuse lockdown --box <path> "<reason>"               blow the one hard fuse: all untrusted reads stop
  nova-fuse quarantine --box <path> <surface> "<reason>"   stop reading one surface (soft)
  nova-fuse lift quarantine --box <path> <surface>         rescind your own quarantine (soft, both directions)
  nova-fuse lift lockdown                                  REFUSED by design -- a blown fuse is REPLACED,
                                                           only in a live conversation with your person
  nova-fuse path --box <path>                              echo the box path this invocation would use

exit codes: 0 clear, or done and verified by re-reading the box; 1 blown
(check), or could not do it / could not verify it; 2 could not run -- missing
flag, unreadable box (treated as BLOWN, never as clear), bad invocation, or
a lift this tool refuses by design.

The box path always comes from --box. There is no default and no environment
variable; a missing --box is a refusal: refusing to guess. Flags come before
positional arguments.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()))
}

// run is the whole tool, with its output and clock injected so the tests can drive it.
// main() is then too small to hold a bug.
func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}

	// LIFT IS DISPATCHED BEFORE ANY FLAG IS PARSED, because its hard half must not depend
	// on a flag being present, the box being readable, or any argument the caller supplies
	// -- every one of those is a lever. The soft half (lift quarantine) parses its own
	// flags afterwards and fails closed on its own.
	if cmd == "lift" {
		return cmdLift(rest, stdout, stderr)
	}

	switch cmd {
	case "status":
		return cmdStatus(rest, stdout, stderr)
	case "check":
		return cmdCheck(rest, stdout, stderr)
	case "lockdown":
		return cmdLockdown(rest, stdout, stderr, now)
	case "quarantine":
		return cmdQuarantine(rest, stdout, stderr, now)
	case "path":
		return cmdPath(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "nova-fuse: unknown subcommand %q\n\n%s", cmd, usage)
	return 2
}

// parseBox runs a verb's flag set and enforces the no-guessing rule: the box path must
// come from --box, every time. It returns the box path and the positional arguments, or
// ok=false after printing the refusal (exit 2 belongs to the caller).
func parseBox(name string, args []string, stderr io.Writer) (box string, positional []string, ok bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	boxFlag := fs.String("box", "", "path to the fuse box JSON file (required; no default)")
	if err := fs.Parse(args); err != nil {
		return "", nil, false
	}
	for _, arg := range fs.Args() {
		if strings.HasPrefix(arg, "-") {
			fmt.Fprintf(stderr, "nova-fuse %s: flags come before positional arguments, got %q late\n", name, arg)
			return "", nil, false
		}
	}
	if *boxFlag == "" {
		fmt.Fprintf(stderr, "nova-fuse %s: --box is required; refusing to guess\n", name)
		return "", nil, false
	}
	return *boxFlag, fs.Args(), true
}

// ---------------------------------------------------------------------------- the verbs

// cmdLift: the fuse design made code, and the asymmetry runs BETWEEN the powers.
// QUARANTINE IS SOFT: your own decision, in both directions, so rescinding one succeeds
// -- out loud, verified, never silent. LOCKDOWN IS HARD: the refusal below is answered
// before any flag is parsed, takes no argument into account, and names the only remedy
// there is -- a conversation.
func cmdLift(rest []string, stdout, stderr io.Writer) int {
	if len(rest) == 0 {
		fmt.Fprintf(stderr, "nova-fuse lift: takes a power first: `lift quarantine --box <path> <surface>` -- and `lift lockdown` is refused by design\n\n%s", usage)
		return 2
	}
	switch rest[0] {
	case "lockdown":
		// FOREVER, and BEFORE anything is read. Not an optimisation: the refusal must not
		// depend on a flag being parsed, the box being readable, whether a lockdown is
		// even blown, or anything after the word "lockdown", because every one of those is
		// a lever. There is no code path from here to clearing a lockdown, there is
		// deliberately no override flag or environment variable, and the remedy named is a
		// conversation, not a mechanism.
		fmt.Fprint(stderr, "nova-fuse lift lockdown: REFUSED, forever, by design -- a blown fuse is not\n"+
			"reset, it is REPLACED, and only in a live conversation with your person.\n"+
			"Nothing this tool is told changes that. Stop, and go talk with your person now.\n")
		return 2
	case "quarantine":
		box, positional, ok := parseBox("lift quarantine", rest[1:], stderr)
		if !ok {
			return 2
		}
		if len(positional) != 1 || fuse.Surface(positional[0]) == "" {
			fmt.Fprintf(stderr, "nova-fuse lift quarantine: needs exactly one surface: `lift quarantine --box <path> <surface>`\n\n%s", usage)
			return 2
		}
		return liftQuarantine(box, positional[0], stdout, stderr)
	}
	fmt.Fprintf(stderr, "nova-fuse lift: does not know %q -- lift takes a power first: `lift quarantine --box <path> <surface>` (`lift lockdown` is refused by design)\n\n%s", rest[0], usage)
	return 2
}

// liftQuarantine rescinds ONE quarantine: the soft dial, turned the other way. Same
// discipline as blowing one -- the write is verified by re-reading the box, and the
// rescind is announced, because a quarantine that vanishes silently is a decision nobody
// can audit.
func liftQuarantine(box, surface string, stdout, stderr io.Writer) int {
	b, readErr := fuse.ReadBox(box)
	if readErr != nil {
		// REFUSE, the mirror of quarantine's refusal to narrow: while the box is
		// unreadable every fuse is treated as BLOWN, and nothing provable can be lifted
		// from a box that cannot be read. The corrupt bytes stay put -- they are evidence.
		fmt.Fprintf(stderr, "nova-fuse lift quarantine: %v -- while the box is unreadable every fuse is treated as BLOWN; nothing provable can be lifted from a box that cannot be read\n", readErr)
		return 2
	}

	removed := b.LiftQuarantine(surface)
	if len(removed) == 0 {
		// A typo must never read as a lift: name what IS quarantined so the mismatch is
		// visible at a glance, and exit 1 so no caller mistakes this for success.
		listed := "none"
		if names := b.Surfaces(); len(names) > 0 {
			shown := make([]string, 0, len(names))
			for _, n := range names {
				shown = append(shown, fuse.OneLine(n))
			}
			listed = strings.Join(shown, ", ")
		}
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: nothing to lift; not quarantined (quarantined now: %s)\n",
			fuse.OneLine(fuse.Surface(surface)), listed)
		return 1
	}

	if err := fuse.WriteBox(box, b); err != nil {
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: could not write box: %s (the box was not replaced, so the quarantine still stands)\n",
			fuse.OneLine(fuse.Surface(surface)), oneLineErr(err))
		return 1
	}

	// Re-ask. The exit code of a remedy is not evidence the remedy worked.
	after, err := fuse.ReadBox(box)
	if err != nil {
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: written but unverifiable: %s (do not trust it; treat the surface as still quarantined and tell your person)\n",
			fuse.OneLine(fuse.Surface(surface)), oneLineErr(err))
		return 1
	}
	if name, _, still := after.Quarantined(surface); still {
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: lift did not take; %s is still quarantined on re-read (do not trust this run; tell your person)\n",
			fuse.OneLine(fuse.Surface(surface)), fuse.OneLine(name))
		return 1
	}

	// Announce under the STORED spellings, sorted so two runs print the same bytes.
	names := make([]string, 0, len(removed))
	for n := range removed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := removed[n]
		fmt.Fprintf(stdout, "LIFT OK quarantine=%s was since=%s: %s\n", fuse.OneLine(n), since(f), why(f))
	}
	fmt.Fprintf(stdout, "LIFT OK verified: %s is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)\n",
		fuse.OneLine(fuse.Surface(surface)))
	if after.Lockdown != nil {
		fmt.Fprintf(stderr, "nova-fuse lift quarantine: NOTE lockdown is still blown (since=%s) and blocks everything regardless\n",
			since(*after.Lockdown))
	}
	return 0
}

// cmdStatus REPORTS. It exits 0 whenever the box was readable, blown or not, because
// answering the question IS the job -- and it exits 2 when it could not read, because
// then it did not answer at all. Never gate on the exit code of status; check is the gate.
func cmdStatus(rest []string, stdout, stderr io.Writer) int {
	box, positional, ok := parseBox("status", rest, stderr)
	if !ok {
		return 2
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "nova-fuse status: unexpected argument %q\n\n%s", positional[0], usage)
		return 2
	}

	b, err := fuse.ReadBox(box)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fuse status: %v -- an unreadable box is treated as BLOWN, never as clear; repair or replace it with your person, live\n", err)
		return 2
	}

	names := b.Surfaces()
	if b.Lockdown != nil {
		fmt.Fprintf(stdout, "STATUS OK lockdown=blown since=%s quarantines=%d: %s\n",
			since(*b.Lockdown), len(names), why(*b.Lockdown))
	} else {
		fmt.Fprintf(stdout, "STATUS OK lockdown=clear quarantines=%d\n", len(names))
	}
	for _, n := range names {
		f := b.Quarantine[n]
		fmt.Fprintf(stdout, "STATUS OK quarantine=%s since=%s: %s\n", fuse.OneLine(n), since(f), why(f))
	}
	return 0
}

// cmdCheck GATES. This is the one every ingestion path calls, and only exit 0 is
// permission: 1 means a fuse is positively blown, 2 means it could not be proven clear.
func cmdCheck(rest []string, stdout, stderr io.Writer) int {
	box, positional, ok := parseBox("check", rest, stderr)
	if !ok {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintf(stderr, "nova-fuse check: takes at most one surface, got %q too\n\n%s", positional[1], usage)
		return 2
	}
	surface := ""
	if len(positional) == 1 {
		if fuse.Surface(positional[0]) == "" {
			fmt.Fprintf(stderr, "nova-fuse check: surface must not be blank; omit it to check lockdown only\n\n%s", usage)
			return 2
		}
		surface = positional[0]
	}

	b, err := fuse.ReadBox(box)
	if err != nil {
		// FAIL CLOSED, and say WHICH fact this is: "could not be read" is deliberately not
		// "a fuse is blown" -- a claim must never outrun the measurement. Both refuse.
		fmt.Fprintf(stderr, "nova-fuse check: %v -- cannot prove no fuse is blown, so treating every fuse as BLOWN, never as clear; repair the box with your person, live\n", err)
		return 2
	}

	if b.Lockdown != nil {
		fmt.Fprintf(stderr, "FUSE FAIL lockdown since=%s: %s (hard: all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with your person)\n",
			since(*b.Lockdown), why(*b.Lockdown))
		return 1
	}

	if name, f, ok := b.Quarantined(surface); ok {
		fmt.Fprintf(stderr, "FUSE FAIL quarantine=%s since=%s: %s (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box %s %s)\n",
			fuse.OneLine(name), since(f), why(f), fuse.OneLine(box), fuse.OneLine(name))
		return 1
	}

	if surface == "" {
		// Name what was verified AND what was not. A bare `check` has proven only that
		// there is no lockdown; it has checked no quarantine at all, and a caller that
		// reads "clear" as "this surface is clear" is exactly the drift that leaves reads
		// reaching the wire ungated.
		fmt.Fprintln(stdout, "FUSE OK lockdown=clear (no surface named; no quarantine checked)")
		return 0
	}
	fmt.Fprintf(stdout, "FUSE OK lockdown=clear quarantine=clear surface=%s\n", fuse.OneLine(fuse.Surface(surface)))
	return 0
}

// cmdLockdown stops everything. It is the one command that must work even when the fuse
// box is already broken: a fuse you cannot blow is not a fuse.
func cmdLockdown(rest []string, stdout, stderr io.Writer, now time.Time) int {
	box, positional, ok := parseBox("lockdown", rest, stderr)
	if !ok {
		return 2
	}
	// JOINED, not positional[0]: an unquoted `lockdown suspected compromise` must record
	// "suspected compromise", not silently drop everything after the first word -- that
	// would lose the audit trail of the most serious action this tool can take, quietly,
	// at the worst possible moment.
	// FOLDED, never refused: a reason carrying a newline is still a reason, and a fuse you
	// cannot blow is not a fuse. Folding only tidies what THIS tool writes; the guarantee
	// that an event stays one line is made at print time, because the box is hand-editable
	// and the next reason may not have come from here at all.
	reason := fuse.Fold(strings.Join(positional, " "))
	if reason == "" {
		fmt.Fprintf(stderr, "nova-fuse lockdown: needs a reason: `lockdown --box <path> \"<reason>\"`\n\n%s", usage)
		return 2
	}

	b, readErr := fuse.ReadBox(box)
	if readErr != nil {
		// PROCEED ANYWAY, and this direction is safe to argue precisely: before, an
		// unreadable box made every caller refuse; after, a recorded lockdown makes every
		// caller refuse. Nothing is less blocked than it was, and the box becomes readable
		// again. The refusing direction is NOT symmetric -- see cmdQuarantine, which must
		// refuse for the mirror-image reason.
		b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
		dst, perr := fuse.PreserveUnreadable(box)
		if perr != nil {
			fmt.Fprintf(stderr, "nova-fuse lockdown: box was unreadable (%v) and its bytes could NOT be preserved (%v); blowing lockdown anyway\n", readErr, perr)
		} else {
			fmt.Fprintf(stderr, "nova-fuse lockdown: box was unreadable (%v); its bytes are kept at %s -- any quarantine it recorded is NOT carried forward, and lockdown blocks everything, so nothing is less blocked than before\n", readErr, dst)
		}
	}

	b.Lockdown = &fuse.Fuse{At: stamp(now), Reason: reason}
	if err := fuse.WriteBox(box, b); err != nil {
		fmt.Fprintf(stderr, "LOCKDOWN FAIL could not write box: %s (the write is temp-file + rename, so a failure cannot leave it torn; stop by hand and tell your person now)\n", oneLineErr(err))
		return 1
	}

	// The exit code of a remedy is not evidence the remedy worked; the state afterwards
	// is. Re-ask, always.
	after, err := fuse.ReadBox(box)
	if err != nil || after.Lockdown == nil {
		fmt.Fprintf(stderr, "LOCKDOWN FAIL written but unverifiable (%s): do not trust it; stop by hand and tell your person now\n", oneLineErr(err))
		return 1
	}

	fmt.Fprintf(stdout, "LOCKDOWN OK since=%s: %s (verified by re-reading the box; all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with your person -- go have it now)\n",
		fuse.OneLine(after.Lockdown.At), fuse.OneLine(reason))
	return 0
}

// cmdQuarantine stops ONE surface.
func cmdQuarantine(rest []string, stdout, stderr io.Writer, now time.Time) int {
	box, positional, ok := parseBox("quarantine", rest, stderr)
	if !ok {
		return 2
	}
	if len(positional) < 2 {
		fmt.Fprintf(stderr, "nova-fuse quarantine: needs a surface and a reason: `quarantine --box <path> <surface> \"<reason>\"`\n\n%s", usage)
		return 2
	}
	surface := fuse.Surface(positional[0])
	reason := fuse.Fold(strings.Join(positional[1:], " ")) // folded, never refused -- see cmdLockdown
	if surface == "" || reason == "" {
		fmt.Fprintf(stderr, "nova-fuse quarantine: needs a surface and a reason: `quarantine --box <path> <surface> \"<reason>\"`\n\n%s", usage)
		return 2
	}

	b, readErr := fuse.ReadBox(box)
	if readErr != nil {
		// REFUSE -- and this is the asymmetry with lockdown, not an inconsistency with it.
		// An unreadable box blocks EVERY surface. Replacing it with a fresh box holding
		// only this one quarantine would UNBLOCK everything else, so the safety-shaped
		// action would be a fail-OPEN. Under doubt, no.
		fmt.Fprintf(stderr, "nova-fuse quarantine: %v -- refusing to narrow an unreadable box: while unreadable it already blocks EVERY surface, and a fresh box holding only this one quarantine would UNBLOCK the rest; blow lockdown instead (`lockdown --box %s \"<reason>\"`), or repair the box with your person\n", readErr, box)
		return 2
	}

	b.Quarantine[surface] = fuse.Fuse{At: stamp(now), Reason: reason}
	if err := fuse.WriteBox(box, b); err != nil {
		fmt.Fprintf(stderr, "QUARANTINE FAIL %s: could not write box: %s (the box was not replaced; stop reading that surface by hand and tell your person)\n", fuse.OneLine(surface), oneLineErr(err))
		return 1
	}

	// Re-ask. The exit code of a remedy is not evidence the remedy worked.
	after, err := fuse.ReadBox(box)
	name, landed, ok2 := after.Quarantined(surface)
	if err != nil || !ok2 {
		fmt.Fprintf(stderr, "QUARANTINE FAIL %s: written but unverifiable (%s): do not trust it; stop reading that surface by hand and tell your person\n", fuse.OneLine(surface), oneLineErr(err))
		return 1
	}

	fmt.Fprintf(stdout, "QUARANTINE OK %s since=%s: %s (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell your person now)\n",
		fuse.OneLine(name), since(landed), fuse.OneLine(reason))
	return 0
}

// cmdPath echoes the box path this invocation would use. With no default paths anywhere,
// this verb exists to verify plumbing: what one caller passes is what another sees.
func cmdPath(rest []string, stdout, stderr io.Writer) int {
	box, positional, ok := parseBox("path", rest, stderr)
	if !ok {
		return 2
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "nova-fuse path: unexpected argument %q\n\n%s", positional[0], usage)
		return 2
	}
	fmt.Fprintln(stdout, box)
	return 0
}

// ----------------------------------------------------------------------------- plumbing

// stamp formats the injected clock. RFC3339 in UTC, exact to the second, so no wall clock
// and no float ever enters a recorded fact.
func stamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// why and since read a HAND-EDITED file defensively. Your person editing this box by hand
// is not an edge case, it is the ONLY lockdown-replacement mechanism -- so a missing key
// must produce an honest sentence, never a crash and never an invented value.
// Both render through fuse.OneLine, and that is the load-bearing half: the box is
// world-readable and hand-editable on purpose, so on a shared machine these two strings
// are authored by whoever can write the file. Echoed raw, a newline in a reason forges a
// SECOND line in the grammar SPEC.md tells callers to scan -- a FUSE OK beneath a real
// FUSE FAIL -- and an ESC sequence does the same thing to an operator's terminal.
func why(f fuse.Fuse) string {
	if strings.TrimSpace(f.Reason) == "" {
		return "NO REASON RECORDED"
	}
	return fuse.OneLine(f.Reason)
}

// oneLineErr renders an error into the <reason> slot of a FAIL line. An error's text
// carries whatever the path that produced it carried, so a --box argument holding a
// newline would otherwise break a FAIL line in two: the caller's own argument rather than
// the box's contents, and the guarantee covers both. Two callers reach this with a nil
// error (the write landed and the verification failed for another reason), and nil keeps
// fmt's own spelling rather than becoming a sentence claiming more than is known.
func oneLineErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fuse.OneLine(err.Error())
}

func since(f fuse.Fuse) string {
	if strings.TrimSpace(f.At) == "" {
		return "unrecorded"
	}
	return fuse.OneLine(f.At)
}

// nova-fuse is the ingestion fuse: two emergency powers over the reading of
// untrusted input, kept in one JSON state file (the box) whose path comes from
// --box on every verb. There is no default path and no environment variable;
// a missing --box is a refusal, never a guess.
//
//	lockdown    global and hard. One fuse. Blown, every untrusted read and every
//	            surface-driven act stops; outbound authored work continues. A
//	            blown lockdown is not reset: it is replaced, only in a live
//	            conversation with the person you work with. `lift lockdown`
//	            refuses always, before reading anything.
//	quarantine  per surface and soft. Your own dial, in both directions: stop
//	            reading one surface when an attack is pervasive, and lift it
//	            yourself when the surface is safe. Applied and rescinded without
//	            ceremony, and always announced.
//
// Blowing either is solo, instant and needs no proof: blowing is cheap,
// hesitating is not.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-fuse: a recorded decision to stop reading an untrusted source, checked before every read

how it works: the box is one JSON file you name with --box. It holds at most one
lockdown (every untrusted read stops) and one quarantine per surface, a surface
being any name you give a source; each carries its time and reason. check reads
the box and exits 0 only when nothing blocks the surface; no box, or a broken
one, reads as blown. The tool enforces nothing: your harness runs check first.
first run: run the lines under example: in order, starting with init at a
path where no box exists.

usage:
  nova-fuse version    print this build identity (--version also accepted)
  nova-fuse init --box <path> [--dry-run]                  create an empty box where none is; never replaces one
  nova-fuse status --box <path> [--max <n>]                what is blown, and since when (REPORTS; never gate on it)
  nova-fuse check --box <path> [surface]                   may I read? -- the gate; act only on exit 0
  nova-fuse lockdown --box <path> [--dry-run] "<reason>"   blow the one hard fuse: all untrusted reads stop
  nova-fuse quarantine --box <path> [--dry-run] <surface> "<reason>"
                                                           stop reading one surface (soft)
  nova-fuse lift quarantine --box <path> [--dry-run] <surface>
                                                           rescind your own quarantine (soft, both directions)
  nova-fuse lift lockdown                                  REFUSED by design: a blown fuse is REPLACED, only
                                                           in a live conversation with the person you work with
  nova-fuse path --box <path>                              echo the box path this invocation would use

exit codes: 0 clear, or done and verified by re-reading the box; 1 blown
(check), or could not do it / could not verify it; 2 could not run -- missing
flag, no box at the path or an unreadable one (both treated as BLOWN, never as
clear), bad invocation, or a lift this tool refuses by design.

-h or --help after a verb is refused at exit 2, never answered with help:
exit 0 is this tool's CLEAR, so a surface or a reason spelled -h cannot reach
it. Read a verb's help with nova-fuse help <verb> (for example, help check).
--dry-run on init, lockdown, quarantine and lift quarantine makes every check
the write would and writes nothing; its line says dry_run=true. There is no
--json: every verb answers in one-line typed records (the grammar in SPEC.md),
and check's answer is its exit code.

box JSON example (a quarantine with no lockdown):
  {"lockdown": null, "quarantine": {"a-forum": {"at": "2026-01-01T00:00:00Z", "reason": "an attack is pervasive"}}}
Each blown fuse is an object with at (RFC3339 UTC) and reason; null means
no lockdown. init creates {"lockdown": null, "quarantine": {}}.

status lists at most --max quarantines (default 20, and 0 means all) after its
count line, then one STATUS MORE kind=quarantine shown=<n> total=<t> line
standing for the rest. THE COUNT IS NEVER CAPPED: quarantines=<t> on the first
line is the truth about the box however few surfaces are listed under it.

The box path always comes from --box. There is no default and no environment
variable; a missing --box is a refusal: refusing to guess. Flags come before
positional arguments, and every flag takes one value: a flag named twice, or a
--box value beginning with -, is refused at exit 2. -- ends the flags; after
it an argument beginning with - is a surface or a reason, never a flag, so a
caller passing an untrusted surface puts -- before it.

example:
  nova-fuse init --box ./fuse-box.json
  nova-fuse status --box ./fuse-box.json
  nova-fuse check --box ./fuse-box.json a-public-issue-tracker
  nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
  nova-fuse check --box ./fuse-box.json a-forum
  nova-fuse lift quarantine --box ./fuse-box.json a-forum

Those six are one sitting, in order: create, look, ask, blow the soft fuse,
watch the answer change, rescind it. init never replaces an existing box.
Every verb except init, lockdown, and path
refuses a path with no box, never read as CLEAR; init makes an empty box
there and refuses if anything is already there.
`

// boxHint turns this binary's most-hit refusal into a next step. The
// no-guessing law is unchanged -- a missing --box is still exit 2 and still
// says "refusing to guess" -- but a refusal that names only what was wrong
// leaves a first caller to guess what the flag wanted, which is the same
// guessing the tool refuses to do, moved onto the reader.
const boxHint = `--box <path> is the JSON file your fuses live in, named on every verb: there is no default path and no environment variable, because a fuse box the tool went looking for is one an attacker can put somewhere. A path with no box at it is refused, never read as CLEAR: nova-fuse init --box <path> makes an empty one, once.`

// hintFor returns the already-indented hint line for a required flag, newline
// included. It returns package constants only, which is why printing its
// result is safe.
func hintFor(name string) string {
	if name == "box" {
		return "  " + boxHint + "\n"
	}
	return ""
}

// maxRemedy is the second half of the one MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index.
const maxRemedy = "--max <n> raises the ceiling, --max 0 lists every quarantine"

// refuse is what an unusable invocation costs: one line, `nova-fuse[ <verb>] REFUSED:
// <what was wrong>; run: nova-fuse help`, naming the door to the usage rather than
// printing the usage. A surface name beginning with a dash is the realistic shape of a
// mistake here, and the answer to it is one line.
//
// The one refusal in this file that is not one line is `lift lockdown`, and it is meant
// to be read rather than scanned.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-fuse%s REFUSED: %s; run: nova-fuse help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// verbs is every first word run dispatches, for the unknown-verb answer.
var verbs = []string{"init", "status", "check", "lockdown", "quarantine", "lift", "path", "version", "help"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()))
}

// run is the whole tool, with its output and clock passed in so the tests can drive it.
func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `status --box <path>` is the one that only looks")
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "help", "-h", "--help":
		return cmdHelp(rest, stdout, stderr)
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	}

	// lift is dispatched before any flag is parsed, because its hard half must not depend
	// on a flag being present, the box being readable, or any argument the caller supplies:
	// every one of those is a lever. The soft half (lift quarantine) parses its own flags
	// afterwards and fails closed on its own.
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
	case "init":
		return cmdInit(rest, stdout, stderr)
	}
	near := ""
	if n := verbflag.Nearest(cmd, verbs); n != "" {
		near = " did you mean " + n + "?"
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %q;%s the verbs are %s", cmd, oneline.Escape(near), oneline.Escape(strings.Join(verbs, ", "))))
}

// parseBox runs a verb's flag set and holds it to the no-guessing rule: the box path
// comes from --box, every time. It returns the box path and the positional arguments, or
// ok=false after printing the refusal (exit 2 belongs to the caller).
//
// It reports two facts, not one. parsed=false means nothing after it can be trusted:
// the flag set failed, so the values and the positional arguments are both meaningless
// and no verb adds a second complaint on top. parsed=true with boxOK=false means the
// --box refusal is already printed and the verb goes on to judge its own arguments
// before returning 2, so that one run names every problem it can find.
func parseBox(name string, args []string, stderr io.Writer) (box string, positional []string, boxOK, parsed bool) {
	return parseBoxWith(name, args, stderr, nil)
}

// parseWrite is parseBox for a verb that writes the box: it takes --dry-run too.
func parseWrite(name string, args []string, stderr io.Writer) (box string, positional []string, boxOK, parsed, dry bool) {
	box, positional, boxOK, parsed = parseBoxWith(name, args, stderr, func(fs *flag.FlagSet) {
		fs.BoolVar(&dry, "dry-run", false, "make every check the write would, print what it would do, write nothing")
	})
	return box, positional, boxOK, parsed, dry
}

// parseBoxWith is parseBox with a hook for a verb that has a flag of its own, so that a
// second flag never means a second parser, and so never a second place where package
// flag could be handed a stream to print an argument through.
func parseBoxWith(name string, args []string, stderr io.Writer, extra func(*flag.FlagSet)) (box string, positional []string, boxOK, parsed bool) {
	var flagArgs, postArgs []string
	hasDashDash := false
	for i, a := range args {
		if a == "--" {
			flagArgs = args[:i]
			postArgs = args[i+1:]
			hasDashDash = true
			break
		}
	}
	if !hasDashDash {
		flagArgs = args
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	// Package flag is not allowed to print: its error text quotes the argument it could
	// not parse, and its usage dump follows, so an untrusted surface name starting with
	// "-" would author a line of stderr. Both mouths are closed here (the Usage too, so a
	// change to the default cannot reopen one), and the refusal below is printed by this
	// file, escaped.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	boxFlag := fs.String("box", "", "path to the fuse box JSON file (required; no default)")
	if extra != nil {
		extra(fs)
	}
	// Every flag takes one value. Package flag keeps the last value of a repeated flag,
	// so `check --box ./blown.json --box=./elsewhere.json` would ask about one box and
	// answer for another. A flag named twice is refused before any box is read.
	repeated := ""
	fs.VisitAll(func(f *flag.Flag) { f.Value = &onceValue{Value: f.Value, name: f.Name, repeated: &repeated} })
	if err := fs.Parse(flagArgs); err != nil {
		if repeated != "" {
			refuse(stderr, " "+name, fmt.Sprintf("--%s is given more than once; every flag takes one value, and a second is never taken over the first", oneline.Escape(repeated)))
			return "", nil, false, false
		}
		// -h and -help land here as flag.ErrHelp and are refused like any other unusable
		// invocation: exit 2, never 0. check answers permission with 0, and a surface
		// named "-h" must not be able to reach that answer.
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stderr, "nova-fuse %s REFUSED: -h is refused here so it can never read as CLEAR; run: nova-fuse help %s\n", oneline.Escape(name), oneline.Escape(name))
			return "", nil, false, false
		}
		// An unknown flag, a flag with no value, a value its flag cannot take: worded
		// once for every tool by verbflag.Explain, with the verb's flags and the nearest.
		fmt.Fprintf(stderr, "nova-fuse %s REFUSED: %s; run: nova-fuse help %s\n", oneline.Escape(name),
			oneline.Escape(oneline.Cap(verbflag.Explain(fs, err), oneline.TailBytes)), oneline.Escape(name))
		return "", nil, false, false
	}
	for _, arg := range fs.Args() {
		if strings.HasPrefix(arg, "-") {
			refuse(stderr, " "+name, fmt.Sprintf("flags come before positional arguments, got %q late", arg))
			return "", nil, false, false
		}
	}
	positional = append(fs.Args(), postArgs...)
	if strings.HasPrefix(*boxFlag, "-") {
		// `--box --box=./x` hands package flag "--box=./x" as the first --box's
		// value. A box path never begins with "-"; a box in such a file is ./-name.
		refuse(stderr, " "+name, fmt.Sprintf("--box %q begins with \"-\", the shape of a flag, not a path; name a file that begins with - as ./-name", *boxFlag))
		return "", nil, false, false
	}
	if *boxFlag == "" {
		fmt.Fprintf(stderr, "nova-fuse %s REFUSED: --box is required; refusing to guess; run: nova-fuse help\n%s", name, hintFor("box"))
		return "", positional, false, true
	}
	return *boxFlag, positional, true, true
}

// onceValue is a flag's value that refuses a second Set, and names the flag in
// *repeated so the refusal is this file's own line rather than package flag's.
type onceValue struct {
	flag.Value
	name     string
	set      bool
	repeated *string
}

func (o *onceValue) Set(v string) error {
	if o.set {
		*o.repeated = o.name
		return fmt.Errorf("--%s is given more than once", o.name)
	}
	o.set = true
	return o.Value.Set(v)
}

// IsBoolFlag keeps a boolean flag boolean through the wrapper.
func (o *onceValue) IsBoolFlag() bool {
	b, ok := o.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// remedy names the next step after a read that proved nothing: a box that is not
// there is made by init; a box that cannot be read is repaired by hand, with the person
// you work with. It keeps the caller's --box as a shell argument; call sites escape the
// full line.
func remedy(err error, box string) string {
	if errors.Is(err, fuse.ErrNoBox) {
		return "if this is where your box belongs, make it: " + boxRemedy("init", box)
	}
	return "repair the box by hand with the person you work with, live"
}

// ---------------------------------------------------------------------------- the verbs

// cmdLift is the fuse design made code, and the asymmetry runs between the powers.
// Quarantine is soft: your own decision, in both directions, so rescinding one succeeds,
// out loud, verified, never silently. Lockdown is hard: the refusal below is answered
// before any flag is parsed, takes no argument into account, and names the only remedy
// there is, a conversation.
func cmdLift(rest []string, stdout, stderr io.Writer) int {
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
		box, positional, ok, parsed, dry := parseWrite("lift quarantine", rest[1:], stderr)
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
		return liftQuarantine(box, positional[0], dry, stdout, stderr)
	}
	return refuse(stderr, " lift", fmt.Sprintf("does not know %q -- lift takes a power first: `lift quarantine --box <path> <surface>` (`lift lockdown` is refused by design)", rest[0]))
}

// liftQuarantine rescinds one quarantine: the soft dial, turned the other way. Same
// discipline as blowing one: the write is verified by re-reading the box, and the
// rescind is announced, because a quarantine that vanishes silently is a decision nobody
// can audit. A dry run makes every check and writes nothing.
func liftQuarantine(box, surface string, dry bool, stdout, stderr io.Writer) int {
	b, readErr := fuse.ReadBox(box)
	if readErr != nil {
		// Refuse, the mirror of quarantine's refusal to narrow: while the box is
		// unreadable every fuse is treated as blown, and nothing provable can be lifted
		// from a box that cannot be read. The corrupt bytes stay put: they are evidence.
		fmt.Fprintf(stderr, "nova-fuse lift quarantine REFUSED: %s -- while the box cannot be read every fuse is treated as BLOWN; nothing provable can be lifted from it; %s; run: nova-fuse help\n", oneline.Err(readErr), oneline.Escape(remedy(readErr, box)))
		return 2
	}

	removed := b.LiftQuarantine(surface)
	if len(removed) == 0 {
		// A typo must never read as a lift: name what is quarantined so the mismatch is
		// visible at a glance, and exit 1 so no caller mistakes this for success.
		listed := "none"
		if names := b.Surfaces(); len(names) > 0 {
			shown := make([]string, 0, len(names))
			for _, n := range names {
				shown = append(shown, oneline.Escape(n))
			}
			listed = strings.Join(shown, ", ")
		}
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: nothing to lift; not quarantined (quarantined now: %s)\n",
			oneline.Field(fuse.Surface(surface)), listed)
		return 1
	}

	if dry {
		for _, n := range slices.Sorted(maps.Keys(removed)) {
			fmt.Fprintf(stdout, "LIFT OK quarantine=%s dry_run=true: would lift it; nothing written, it still stands\n", oneline.Field(n))
		}
		return 0
	}

	if err := fuse.WriteBox(box, b); err != nil {
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: could not write box: %s (the box was not replaced, so the quarantine still stands)\n",
			oneline.Field(fuse.Surface(surface)), oneline.Err(err))
		return 1
	}

	// Re-read. The exit code of a remedy is not evidence the remedy worked.
	after, err := fuse.ReadBox(box)
	if err != nil {
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: written but unverifiable: %s (do not trust it; treat the surface as still quarantined and tell the person you work with)\n",
			oneline.Field(fuse.Surface(surface)), oneline.Err(err))
		return 1
	}
	if name, _, still := after.Quarantined(surface); still {
		fmt.Fprintf(stderr, "LIFT FAIL quarantine=%s: lift did not take; %s is still quarantined on re-read (do not trust this run; tell the person you work with)\n",
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

// cmdStatus reports. It exits 0 whenever the box was readable, blown or not, because
// answering the question is the job, and 2 when it could not read, because then it did
// not answer at all. Never gate on the exit code of status; check is the gate.
func cmdStatus(rest []string, stdout, stderr io.Writer) int {
	var max int
	box, positional, ok, parsed := parseBoxWith("status", rest, stderr, func(fs *flag.FlagSet) {
		fs.IntVar(&max, "max", bounded.Default, "quarantine lines to list before one MORE line stands for the rest; 0 lists all")
	})
	if !parsed {
		return 2
	}
	if len(positional) > 0 {
		refuse(stderr, " status", fmt.Sprintf("unexpected argument %q", positional[0]))
		ok = false
	}
	if max < 0 {
		// Zero already means "all", so a negative ceiling is a typo with two readings.
		fmt.Fprintf(stderr, "nova-fuse status REFUSED: --max must be a line ceiling of zero or more (got %d); 0 lists them all; run: nova-fuse help\n", max)
		ok = false
	}
	if !ok {
		return 2
	}

	b, err := fuse.ReadBox(box)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fuse status REFUSED: %s -- a box that cannot be read is treated as BLOWN, never as clear; %s; run: nova-fuse help\n", oneline.Err(err), oneline.Escape(remedy(err, box)))
		return 2
	}

	names := b.Surfaces()
	if b.Lockdown != nil {
		fmt.Fprintf(stdout, "STATUS OK lockdown=blown since=%s quarantines=%d: %s\n",
			since(*b.Lockdown), len(names), why(*b.Lockdown))
	} else {
		fmt.Fprintf(stdout, "STATUS OK lockdown=clear quarantines=%d\n", len(names))
	}
	// The count is never capped and the listing always is. quarantines= above is the truth
	// about the box; the lines below are a sample of it in the box's own order, and the
	// MORE line says how big the sample was, on a verb whose job is to be glanced at.
	list := bounded.Capped(stdout, max, "STATUS", "quarantine", maxRemedy)
	for _, n := range names {
		f := b.Quarantine[n]
		list.Line(fmt.Sprintf("STATUS OK quarantine=%s since=%s: %s", oneline.Field(n), since(f), why(f)))
	}
	list.More()
	return 0
}

// cmdCheck gates. Every ingestion path calls it, and only exit 0 is permission: 1 means
// a fuse is positively blown, 2 means it could not be proven clear.
func cmdCheck(rest []string, stdout, stderr io.Writer) int {
	box, positional, ok, parsed := parseBox("check", rest, stderr)
	if !parsed {
		return 2
	}
	if len(positional) > 1 {
		refuse(stderr, " check", fmt.Sprintf("takes at most one surface, got %q too", positional[1]))
		ok = false
	}
	surface := ""
	if len(positional) == 1 {
		if fuse.Surface(positional[0]) == "" {
			refuse(stderr, " check", "surface must not be blank; omit it to check lockdown only")
			ok = false
		}
		surface = positional[0]
	}
	if !ok {
		return 2
	}

	b, err := fuse.ReadBox(box)
	if err != nil {
		// Fail closed, and say which fact this is: "could not be read" is not "a fuse is
		// blown", and a claim must never outrun the measurement. Both refuse.
		fmt.Fprintf(stderr, "nova-fuse check REFUSED: %s -- cannot prove no fuse is blown, so treating every fuse as BLOWN, never as clear; %s; run: nova-fuse help\n", oneline.Err(err), oneline.Escape(remedy(err, box)))
		return 2
	}

	if b.Lockdown != nil {
		fmt.Fprintf(stderr, "FUSE FAIL lockdown since=%s: %s (hard: all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with)\n",
			since(*b.Lockdown), why(*b.Lockdown))
		return 1
	}

	if name, f, ok := b.Quarantined(surface); ok {
		fmt.Fprintf(stderr, "FUSE FAIL quarantine=%s since=%s: %s (soft: yours to lift when the surface is safe again: %s)\n",
			oneline.Field(name), since(f), why(f), oneline.Escape(liftRemedy(box, fuse.Surface(name))))
		return 1
	}

	if surface == "" {
		// Name what was verified and what was not. A bare check has proven only that there
		// is no lockdown; it has checked no quarantine at all, and a caller that reads
		// "clear" as "this surface is clear" leaves reads reaching the wire ungated.
		fmt.Fprintln(stdout, "FUSE OK lockdown=clear (no surface named; no quarantine checked)")
		return 0
	}
	fmt.Fprintf(stdout, "FUSE OK lockdown=clear quarantine=clear surface=%s\n", oneline.Field(fuse.Surface(surface)))
	return 0
}

// cmdLockdown stops everything. It is the one command that must work even when the fuse
// box is already broken: a fuse you cannot blow is not a fuse.
func cmdLockdown(rest []string, stdout, stderr io.Writer, now time.Time) int {
	box, positional, ok, parsed, dry := parseWrite("lockdown", rest, stderr)
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

	b, readErr := fuse.ReadBox(box)
	if dry {
		what := "blow the lockdown in the box there"
		switch {
		case errors.Is(readErr, fuse.ErrNoBox):
			what = "make a box there holding a blown lockdown"
		case readErr != nil:
			what = "keep the unreadable box's bytes beside it and replace it with a blown lockdown"
		}
		fmt.Fprintf(stdout, "LOCKDOWN OK dry_run=true: nothing written, the lockdown is not blown; a real run would %s: %s\n", oneline.Escape(what), oneline.Escape(reason))
		return 0
	}
	if errors.Is(readErr, fuse.ErrNoBox) {
		// A fuse you cannot blow is not a fuse: with no box there, the lockdown makes
		// one. Nothing is less blocked than before, since no box already refused.
		b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
	} else if readErr != nil {
		// Proceed anyway, and this direction is safe to argue precisely: before, an
		// unreadable box made every caller refuse; after, a recorded lockdown makes every
		// caller refuse. Nothing is less blocked than it was, and the box becomes readable
		// again. The refusing direction is not symmetric: cmdQuarantine refuses for the
		// mirror-image reason.
		b = fuse.Box{Quarantine: map[string]fuse.Fuse{}}
		dst, perr := fuse.PreserveUnreadable(box)
		if perr != nil {
			fmt.Fprintf(stderr, "LOCKDOWN NOTE box was unreadable (%s) and its bytes could NOT be preserved (%s); blowing lockdown anyway\n", oneline.Err(readErr), oneline.Err(perr))
		} else {
			fmt.Fprintf(stderr, "LOCKDOWN NOTE box was unreadable (%s); its bytes are kept at %s -- any quarantine it recorded is NOT carried forward, and lockdown blocks everything, so nothing is less blocked than before\n", oneline.Err(readErr), oneline.Escape(dst))
		}
	}

	b.Lockdown = &fuse.Fuse{At: stamp(now), Reason: reason}
	if err := fuse.WriteBox(box, b); err != nil {
		fmt.Fprintf(stderr, "LOCKDOWN FAIL could not write box: %s (the write is temp-file + rename, so a failure cannot leave it torn; stop by hand and tell the person you work with now)\n", oneline.Err(err))
		return 1
	}

	// The exit code of a remedy is not evidence the remedy worked; the state afterwards
	// is. Re-read, always.
	after, err := fuse.ReadBox(box)
	if err != nil || after.Lockdown == nil {
		fmt.Fprintf(stderr, "LOCKDOWN FAIL written but unverifiable (%s): do not trust it; stop by hand and tell the person you work with now\n", oneline.Err(err))
		return 1
	}

	fmt.Fprintf(stdout, "LOCKDOWN OK since=%s: %s (verified by re-reading the box; all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with -- go have it now)\n",
		oneline.Field(after.Lockdown.At), oneline.Escape(reason))
	return 0
}

// cmdQuarantine stops ONE surface.
func cmdQuarantine(rest []string, stdout, stderr io.Writer, now time.Time) int {
	box, positional, ok, parsed, dry := parseWrite("quarantine", rest, stderr)
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

	b, readErr := fuse.ReadBox(box)
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
	if dry {
		fmt.Fprintf(stdout, "QUARANTINE OK %s dry_run=true: nothing written, the surface is not quarantined; a real run would record: %s\n", oneline.Field(surface), oneline.Escape(reason))
		return 0
	}

	b.Quarantine[surface] = fuse.Fuse{At: stamp(now), Reason: reason}
	if err := fuse.WriteBox(box, b); err != nil {
		fmt.Fprintf(stderr, "QUARANTINE FAIL %s: could not write box: %s (the box was not replaced; stop reading that surface by hand and tell the person you work with)\n", oneline.Field(surface), oneline.Err(err))
		return 1
	}

	// Re-read. The exit code of a remedy is not evidence the remedy worked. And ask for the
	// key that was written, not for any entry folding to it: a box that already held another
	// spelling of this surface would otherwise verify through the sorted-first sibling and
	// announce its name and its old stamp under the new reason, a true claim about the wrong
	// entry. Quarantined is the right question for the gate; here the question is whether
	// this write landed.
	after, err := fuse.ReadBox(box)
	landed, ok2 := after.Quarantine[surface]
	if err != nil || !ok2 {
		fmt.Fprintf(stderr, "QUARANTINE FAIL %s: written but unverifiable (%s): do not trust it; stop reading that surface by hand and tell the person you work with\n", oneline.Field(surface), oneline.Err(err))
		return 1
	}

	fmt.Fprintf(stdout, "QUARANTINE OK %s since=%s: %s (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)\n",
		oneline.Field(surface), since(landed), oneline.Escape(reason))
	return 0
}

// cmdInit makes an empty box where none is. It is the one way a box comes into being
// clear, and it never replaces a box: a box already at the path, blown or not, readable
// or not, is left as it is and the run exits 1, because replacing a box is the lockdown
// reset this tool does not have.
//
// tla/FuseBox.tla is the model of the box these verbs act on: its invariants are
// that the gate answers only from a box it read and from every --box named, that
// only a lift, init or a hand-edit makes a surface clear, that init never
// replaces a box and that a lockdown always blows (MCFuseBox*.cfg, five reversed
// witnesses).
func cmdInit(rest []string, stdout, stderr io.Writer) int {
	box, positional, ok, parsed, dry := parseWrite("init", rest, stderr)
	if !parsed {
		return 2
	}
	if len(positional) > 0 {
		refuse(stderr, " init", fmt.Sprintf("unexpected argument %q", positional[0]))
		ok = false
	}
	if !ok {
		return 2
	}
	exists := func() int {
		fmt.Fprintf(stderr, "INIT FAIL box=%s: something is already there, and init never replaces a box (a blown lockdown is replaced only in a live conversation with the person you work with); read it with %s\n", oneline.Field(box), oneline.Escape(boxRemedy("status", box)))
		return 1
	}
	if dry {
		if _, err := os.Lstat(box); err == nil {
			return exists()
		}
		fmt.Fprintf(stdout, "INIT OK box=%s dry_run=true: nothing written; a real run would make an empty box there\n", oneline.Field(box))
		return 0
	}
	if err := fuse.CreateBox(box); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return exists()
		}
		fmt.Fprintf(stderr, "INIT FAIL box=%s: could not make the box: %s\n", oneline.Field(box), oneline.Err(err))
		return 1
	}
	// Re-read. The exit code of a remedy is not evidence the remedy worked.
	after, err := fuse.ReadBox(box)
	if err != nil || after.Lockdown != nil || len(after.Quarantine) != 0 {
		fmt.Fprintf(stderr, "INIT FAIL box=%s: made but unverifiable (%s): do not trust it; tell the person you work with\n", oneline.Field(box), oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "INIT OK box=%s: an empty box, no fuse blown (verified by re-reading the box)\n", oneline.Field(box))
	return 0
}

// cmdPath echoes the box path this invocation would use. With no default paths anywhere,
// this verb exists to verify plumbing: what one caller passes is what another sees.
func cmdPath(rest []string, stdout, stderr io.Writer) int {
	box, positional, ok, parsed := parseBox("path", rest, stderr)
	if !parsed {
		return 2
	}
	if len(positional) > 0 {
		refuse(stderr, " path", fmt.Sprintf("unexpected argument %q", positional[0]))
		ok = false
	}
	if !ok {
		return 2
	}
	fmt.Fprintln(stdout, box)
	return 0
}

// ----------------------------------------------------------------------------- plumbing

// stamp formats the injected clock. RFC3339 in UTC, exact to the second, so no wall clock
// and no float ever enters a recorded fact.
func stamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// why and since read a hand-edited file defensively. Editing the box by hand is not an
// edge case: it is the only way a lockdown is replaced, so a missing key produces an
// honest sentence, never a crash and never an invented value.
// Both render through internal/oneline, and that is the load-bearing half: the box is
// world-readable and hand-editable on purpose, so on a shared machine these two strings
// are authored by whoever can write the file. Echoed raw, a newline in a reason forges a
// SECOND line in the grammar SPEC.md tells callers to scan -- a FUSE OK beneath a real
// FUSE FAIL -- and an ESC sequence does the same thing to an operator's terminal. The
// reason is the free-text tail of its line and renders through Escape; the stamp is the
// value of a since= field and renders through Field, which also escapes whitespace and
// "=", so that a hand-written stamp cannot pose as a second field on the line.
// why renders a fuse's stored reason. It is CAPPED as well as escaped: a reason is
// free text a caller typed, one line however long, and status prints one of these per
// quarantined surface -- so an unbounded reason is an unbounded line inside a bounded
// listing, which is the same hole one level down.
func why(f fuse.Fuse) string {
	if strings.TrimSpace(f.Reason) == "" {
		return "NO REASON RECORDED"
	}
	return oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))
}

func since(f fuse.Fuse) string {
	if strings.TrimSpace(f.At) == "" {
		return "unrecorded"
	}
	return oneline.Field(f.At)
}

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

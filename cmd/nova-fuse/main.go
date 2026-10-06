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
	"os"
	"path/filepath"
	"strings"
	"time"

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
  nova-fuse lift lockdown                                  REFUSED by design: the box is replaced: nova-fuse
                                                           init --box <a new path>, and the harness pointed at
                                                           it, by the person, never by this tool
  nova-fuse path --box <path>                              echo the box path this invocation would use

exit codes: 0 clear, or done and verified by re-reading the box; by verb:
check: 1 blown (a lockdown, or a quarantine on the surface); init, lockdown,
quarantine, lift quarantine: 1 the write was attempted and re-reading the box
did not show it; status, path: 0 only; every verb: 2 could not run -- missing
flag, bad invocation, or a lift this tool refuses by design; check, status,
quarantine and lift quarantine also answer 2 at a path with no box or an
unreadable box, never read as clear.

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
Every verb except init, lockdown, and path refuses a path with no box, never
read as CLEAR; init makes an empty box there and refuses if anything is
already there, lockdown makes a blown box there in one write, and path reads
no box at all.
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

// invocation is the process state one run reads. main passes the process's
// own; a test passes its own, so tests run in parallel (docs/STANDARD.md
// section 8). stamp is the release -ldflags var, not an environment variable.
type invocation struct {
	getenv func(string) string
	wd     string
	stamp  string
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC(), invocation{
		getenv: os.Getenv,
		wd:     wd,
		stamp:  version,
	}))
}

// run is the whole tool, with its output, clock and process state passed in so
// the tests can drive it.
func run(args []string, stdout, stderr io.Writer, now time.Time, inv invocation) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `status --box <path>` is the one that only looks")
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "help", "-h", "--help":
		return cmdHelp(rest, stdout, stderr)
	case "version", "--version":
		return cmdVersionWith(rest, stdout, stderr, inv.stamp)
	}

	// lift is dispatched before any flag is parsed, because its hard half must not depend
	// on a flag being present, the box being readable, or any argument the caller supplies:
	// every one of those is a lever. The soft half (lift quarantine) parses its own flags
	// afterwards and fails closed on its own.
	if cmd == "lift" {
		return cmdLift(rest, stdout, stderr, inv)
	}

	switch cmd {
	case "status":
		return cmdStatus(rest, stdout, stderr, inv)
	case "check":
		return cmdCheck(rest, stdout, stderr, inv)
	case "lockdown":
		return cmdLockdown(rest, stdout, stderr, now, inv)
	case "quarantine":
		return cmdQuarantine(rest, stdout, stderr, now, inv)
	case "path":
		return cmdPath(rest, stdout, stderr, inv)
	case "init":
		return cmdInit(rest, stdout, stderr, inv)
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
func parseBox(name string, args []string, stderr io.Writer, getenv func(string) string) (box string, positional []string, boxOK, parsed bool) {
	return parseBoxWith(name, args, stderr, getenv, nil)
}

// parseWrite is parseBox for a verb that writes the box: it takes --dry-run too.
func parseWrite(name string, args []string, stderr io.Writer, getenv func(string) string) (box string, positional []string, boxOK, parsed, dry bool) {
	box, positional, boxOK, parsed = parseBoxWith(name, args, stderr, getenv, func(fs *flag.FlagSet) {
		fs.BoolVar(&dry, "dry-run", false, "make every check the write would, print what it would do, write nothing")
	})
	return box, positional, boxOK, parsed, dry
}

// parseBoxWith is parseBox with a hook for a verb that has a flag of its own, so that a
// second flag never means a second parser, and so never a second place where package
// flag could be handed a stream to print an argument through.
func parseBoxWith(name string, args []string, stderr io.Writer, getenv func(string) string, extra func(*flag.FlagSet)) (box string, positional []string, boxOK, parsed bool) {
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
	// The path is the flag. NOVA_FUSE_BOX is read and never used: a missing
	// flag stays a refusal, and a flag that was given is not replaced
	// (docs/STANDARD.md section 8).
	chosen := boxPath(*boxFlag, getenv)
	if chosen == "" {
		fmt.Fprintf(stderr, "nova-fuse %s REFUSED: --box is required; refusing to guess; run: nova-fuse help\n%s", name, hintFor("box"))
		return "", positional, false, true
	}
	return chosen, positional, true, true
}

// boxPath is the box this invocation uses. It is the --box flag. A set
// NOVA_FUSE_BOX does not fill a missing flag and does not replace one that
// was given (docs/STANDARD.md section 8).
func boxPath(flagValue string, getenv func(string) string) string {
	if flagValue != "" {
		return flagValue
	}
	if getenv != nil && getenv("NOVA_FUSE_BOX") != "" {
		return ""
	}
	return ""
}

// boxFile is the path fuse opens for a --box value. A relative value is opened
// against wd. When wd is the process working directory, the caller's path is
// kept so every printed line stays the words that were typed (docs/STANDARD.md
// section 8).
func boxFile(wd, box string) string {
	if wd == "" || box == "" || filepath.IsAbs(box) {
		return box
	}
	cwd, err := os.Getwd()
	if err == nil && wd == cwd {
		return box
	}
	return filepath.Join(wd, box)
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
// FUSE FAILED -- and an ESC sequence does the same thing to an operator's terminal. The
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

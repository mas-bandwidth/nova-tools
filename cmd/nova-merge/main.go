// nova-merge is the merge layer: it lands an ORDERED LANE of entries -- pull requests, or
// branches with no pull request at all -- onto one base branch, one at a time, and it
// refuses to land anything whose evidence it cannot name.
//
// A lane in this shape landed 30-odd pull requests onto one base in a single morning. The
// form works, and it fails in every way a shell loop around `gh pr merge` fails. This
// tool is those failures closed, one rule each (docs/SPEC-MERGE.md):
//
//	init        creates the lane, once, with its repository, its base and its branch
//	add         queues a pull request; add-branch queues a branch with no pull request
//	read        a reader's own verdict, for the sha THEY read, as one file in the branch
//	gate        a gate runner's verdict for ONE integration commit, named by sha
//	run         one pass: the base's evidence, the order, at most one merge, then stop
//	status      the lane, in counts, without running a pass
//	dry-run     every read a pass performs and the whole plan, and no path to a mutation
//	packet      the smallest sufficient review packet: pointers, never the diff
//	stop        start nothing new
//
// Everything this tool reads from the host is DATA. A pull request body, a check name, a
// branch name, a commit subject: none of them is an instruction and none is a grant. A
// read verdict is the only thing that authorizes a merge, and a read verdict is recorded
// by a line at a keyboard.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-merge: an ordered lane onto one base, with the races taken out (see docs/SPEC-MERGE.md)

usage:
  nova-merge init       --lane <dir> --repo <owner>/<name> --base <branch> --lane-branch <name>
  nova-merge add        --lane <dir> --pr <n> [--needs-read]
  nova-merge add-branch --lane <dir> --branch <name> [--needs-read]
  nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --verdict approve|hold [--note <text>]
  nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --base-sha <sha> --merge <sha> --verdict green|red --summary <path>
  nova-merge run        --lane <dir> (--once | --loop <duration> --hours <h>) [--planned-red <text>] [--max <n>]
  nova-merge status     --lane <dir> [--max <n>] [--reads <entry>]
  nova-merge dry-run    --lane <dir> [--max <n>]
  nova-merge packet     --lane <dir> --who <name> ((--pr <n>|--branch <name>) | --all) [--max <n>]
  nova-merge quickstart --lane <dir> --repo <owner>/<name> --base <branch> --lane-branch <name>
  nova-merge stop       --lane <dir>

every verb that runs git or gh also takes [--timeout <seconds>], default 120.

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a merge that
could not be landed, a merge that RACED, a publication the remote refused, an entry
STOPPED, a pass that ended with at least one BLOCKED entry, an init of a lane that
exists; 2 could not run: missing flag, unreadable lane state, a directory that is
not a lane, bad invocation, git or gh absent.

AN ENTRY THAT IS MERELY WAITING IS NOT A FAILURE. Zero pending checks is not the
same news as a red check, and a lane full of entries waiting on CI is a lane
working exactly as intended: run exits 0 with waiting=<n>.

No guessed anything, with one named exception. There is no default lane
directory, no default repository and no default base; a missing one is exit 2 and
refusing to guess. --timeout defaults to 120 seconds, because a subprocess
timeout is how long this tool waits before saying so rather than a fact about a
lane that only its owner can supply. --loop gets no default interval and --hours
no default deadline for the opposite reason: a loop with no deadline is a lane
that is stuck rather than working, and nobody outside can tell the two apart.

The repository, the base and the lane branch are properties of the LANE, written
once by init. No other verb takes --repo, --base or --lane-branch, and the flag
on gate that names the base SHA is spelled --base-sha so the two are never one
word.

ONE PREDICATE. An entry merges on the NEWEST gate record for (its head, the base
sha read this pass) being green, that gate's merge being an object in the lane's
clone whose parents are exactly that base and that head, the read condition, the
four placement conditions, and -- on main -- no hosted red. Hosted green and a
gate for the head alone are CANDIDATES: they earn the entry NEEDS-GATE, the
build, and the gate command on RUN NOTE, and nothing else.

A read and a gate are each ONE IMMUTABLE FILE in the lane's branch, written to a
durable outbox first and pushed by the tool in a compare-and-swap loop, so a
reader on another machine records a verdict where every lane on that branch will
fold it. state.json is the fold and the files are the truth: losing it loses the
order and nothing else.

The lane never edits an entry's content. A conflict is BLOCKED with every
conflicting file named and the exact hand command on the line; no code path here
writes a resolved file.

example:
  nova-merge quickstart --lane ./lane --repo mas-bandwidth/nova-tools --base main --lane-branch nova-merge/main
  nova-merge add --lane ./lane --pr 949 --needs-read
  nova-merge status --lane ./lane
  nova-merge packet --lane ./lane --who emma --all
  nova-merge dry-run --lane ./lane

Those five are one sitting, in order: make the lane, queue an entry, look at it,
ask what a reader would be handed, and see what a pass would do without doing it.
./lane is a path of yours and nothing is guessed from it.
`

// refuse is what an unusable invocation costs: ONE line naming what was wrong and the
// door to the usage, never the banner, which is 90-odd lines. A harness reading this
// tool's stderr pays that on every typo.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-merge%s: %s; run: nova-merge help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, production()))
}

// Deps is everything this binary reaches outside itself: the clock, the sleep, the git
// URL of a repository, the host, and the id of the binary on disk. It is injected so that
// the tests drive a real git against a bare fixture repository and a fake host, and reach
// no network at all.
type Deps struct {
	Now     func() time.Time
	Sleep   func(time.Duration)
	RepoURL func(repo string) string
	NewHost func(repo string, timeout time.Duration) merge.Host
	Runner  merge.Runner
	BuildID func() string
}

func production() Deps {
	return Deps{
		Now:     func() time.Time { return time.Now().UTC() },
		Sleep:   time.Sleep,
		RepoURL: func(repo string) string { return "https://github.com/" + repo + ".git" },
		NewHost: func(repo string, timeout time.Duration) merge.Host {
			return merge.NewGH(repo, timeout, nil)
		},
		BuildID: buildID,
	}
}

// buildID is the id of the binary at this tool's own path: twelve hex of the sha256 of
// its bytes.
//
// It is a HASH OF THE FILE rather than a stamped constant for rule 16's reason. The loop
// has to answer "is the binary on disk the one I am running", and a hand-maintained
// constant answers that with what somebody remembered to write. The file's own bytes
// cannot be wrong about themselves, and a build with no readable path says devel rather
// than inventing a number.
func buildID() string {
	path, err := os.Executable()
	if err != nil {
		return "devel"
	}
	f, err := os.Open(path)
	if err != nil {
		return "devel"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "devel"
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func run(args []string, stdout, stderr io.Writer, deps Deps) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `status --lane <dir>` is the one that only looks")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "nova-merge %s\n", oneline.Field(deps.BuildID()))
		return 0
	}
	// The three lane properties and the one base-sha spelling, refused by NAME off the
	// verb that owns them, before anything is parsed: a --base on a queueing verb would
	// let two invocations disagree about where the lane lands, and --base beside
	// --base-sha is one word away from a gate for the wrong commit.
	if code, bad := foreignFlags(verb, rest, stderr); bad {
		return code
	}
	switch verb {
	case "init":
		return cmdInit(rest, stdout, stderr, deps, false)
	case "quickstart":
		return cmdInit(rest, stdout, stderr, deps, true)
	case "add":
		return cmdAdd(rest, stdout, stderr, deps, false)
	case "add-branch":
		return cmdAdd(rest, stdout, stderr, deps, true)
	case "read":
		return cmdRead(rest, stdout, stderr, deps)
	case "gate":
		return cmdGate(rest, stdout, stderr, deps)
	case "run":
		return cmdRun(rest, stdout, stderr, deps)
	case "status":
		return cmdStatus(rest, stdout, stderr, deps)
	case "dry-run":
		return cmdDryRun(rest, stdout, stderr, deps)
	case "packet":
		return cmdPacket(rest, stdout, stderr, deps)
	case "stop":
		return cmdStop(rest, stdout, stderr, deps)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", verb))
}

// foreignFlags refuses a flag on a verb that does not own it, naming the verb that does.
func foreignFlags(verb string, args []string, stderr io.Writer) (int, bool) {
	has := func(name string) bool {
		for _, a := range args {
			flagName, _, _ := strings.Cut(a, "=")
			if flagName == "--"+name || flagName == "-"+name {
				return true
			}
		}
		return false
	}
	creation := verb == "init" || verb == "quickstart"
	for _, name := range []string{"repo", "lane-branch"} {
		if !creation && has(name) {
			return refuse(stderr, " "+verb, fmt.Sprintf("--%s belongs to `init`, which writes it into the lane once; every other verb reads it from the lane's state", name)), true
		}
	}
	if !creation && has("base") {
		if verb == "gate" {
			return refuse(stderr, " gate", "--base is the lane's branch and belongs to `init`; the base SHA a gate was taken against is --base-sha, a different word on purpose"), true
		}
		return refuse(stderr, " "+verb, "--base belongs to `init`, which writes it into the lane once; a --base here would let two invocations disagree about where the lane lands"), true
	}
	if verb != "gate" && has("base-sha") {
		return refuse(stderr, " "+verb, "--base-sha names the base a GATE was taken against and belongs to `gate`"), true
	}
	return 0, false
}

// flags is one verb's flag set with package flag's two mouths closed: its error text
// quotes the argument it could not parse and its usage dump follows, so an argument
// beginning with a dash could otherwise author a whole line of stderr before any code
// here ran.
type flags struct {
	verb     string
	fs       *flag.FlagSet
	problems []string
}

func newFlags(verb string) *flags {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flags{verb: verb, fs: fs}
}

// problem collects one independent problem. A REFUSAL PRINTS EVERY INDEPENDENT PROBLEM
// IN ONE GO: sending a first run back three times for three flags is three refusals the
// first one already knew about.
func (f *flags) problem(format string, args ...any) {
	f.problems = append(f.problems, fmt.Sprintf(format, args...))
}

func (f *flags) parse(args []string, stderr io.Writer) bool {
	if err := f.fs.Parse(args); err != nil {
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		f.problem("takes no positional arguments, got %d (flags come before arguments)", n)
	}
	return true
}

// require is the no-guessing law, and it says what the flag WANTS rather than only that
// it is missing.
func (f *flags) require(name, value, wants string) {
	if strings.TrimSpace(value) == "" {
		f.problem("--%s is required; refusing to guess: %s", name, wants)
	}
}

// done prints every problem this parse found, one line each, and returns whether the verb
// may run.
func (f *flags) done(stderr io.Writer) bool {
	if len(f.problems) == 0 {
		return true
	}
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-merge %s: %s\n", f.verb, oneline.Escape(oneline.Cap(p, oneline.TailBytes)))
	}
	fmt.Fprintf(stderr, "  run: nova-merge help\n")
	return false
}

// laneFlags are the two every verb has.
type laneFlags struct {
	*flags
	lane    *string
	timeout *int
	max     *int
}

func laneSet(verb string) *laneFlags {
	f := newFlags(verb)
	return &laneFlags{
		flags:   f,
		lane:    f.fs.String("lane", "", ""),
		timeout: f.fs.Int("timeout", 120, ""),
		max:     f.fs.Int("max", bounded.Default, ""),
	}
}

func (l *laneFlags) check() {
	l.require("lane", *l.lane, "the lane's own directory, which holds its state, its clone and its records")
	if *l.timeout < 1 {
		l.problem("--timeout is a number of seconds this tool waits for git or gh before saying so, and is at least 1, got %d", *l.timeout)
	}
	if *l.max < 0 {
		l.problem("--max is a ceiling on a listing: 0 means all and a negative one is a typo with two readings, got %d", *l.max)
	}
}

func (l *laneFlags) dur() time.Duration { return time.Duration(*l.timeout) * time.Second }

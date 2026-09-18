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

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/cliflags"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-merge: an ordered lane onto one base, with the races taken out (see docs/SPEC-MERGE.md)

usage:
  nova-merge version    print this build identity (--version also accepted)
  nova-merge init       --lane <dir> --repo <owner>/<name> --base <branch> --lane-branch <name> [--remote <url>]
  nova-merge add        --lane <dir> --pr <n> [--needs-read]
  nova-merge add-branch --lane <dir> --branch <name> [--needs-read]
  nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --verdict approve|hold [--note <text>]
  nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --base-sha <sha> --merge <sha> --verdict green|red --summary <path>
  nova-merge run        --lane <dir> (--once | --loop <duration> --hours <h>) [--planned-red <text>] [--admin] [--max <n>]
  nova-merge status     --lane <dir> [--max <n>] [--reads <entry>]
  nova-merge dry-run    --lane <dir> [--max <n>]
  nova-merge packet     --lane <dir> --who <name> ((--pr <n>|--branch <name>) | --all) [--max <n>] [--decide [--floor <0-1>] [--card <file>] [--key-env <var>] [--base-url <url>]]
  nova-merge quickstart --lane <dir> --repo <owner>/<name> --base <branch> --lane-branch <name> [--remote <url>]
  nova-merge stop       --lane <dir>
  nova-merge classify   --lane <dir> --run <id> [--base-url <url>] [--key-env <name>]
  nova-merge wait       --repo <owner>/<name> --pr <n> --timeout <duration> [--interval <duration>]
  nova-merge sweep      --repo <owner>/<name> --branch <branch> --once [--prefix <head-prefix>] [--timeout <seconds>]
  nova-merge simulate   --repo <path> --base <branch> [--entries <file>] [--checks "<a>,<b>"] [--timeout <duration>]
  nova-merge rebase     --once --repo <owner>/<name> --markers <dir> --out <dir> --queue <dir> [--base <branch>]
  nova-merge react      --redis <addr> --lane <dir> (--once | --deadline <seconds>) [--timeout <seconds>]
  nova-merge batch      --name <name> --pr <list> --repo <owner>/<name> --root <dir> [--base <branch>] [--reference <mirror>] [--timeout <duration>] [--gomaxprocs <n>] [--require-lisp] [--no-require-checks] [--receipt-file <path>]
  nova-merge land       --repo <owner>/<name> --pr <n> [--receipt <line> | --receipt-file <path>] [--no-jump] [--timeout <seconds>]

  nova-merge queue    --lane <dir> (status|hold <reason> --who <name>|release|skip <pr>...|unskip <pr>...|front <pr>|sweep) [--window <duration>] [--max <n>]
  nova-merge queue audit --repo <owner>/<name> [--dry-run] [--timeout <seconds>]
  nova-merge queue classify --lane <dir> --run <id> --verdict flaky-under-load|own-change|environment [--note <text>]

queue classify RECORDS a verdict somebody already reached, as one immutable record the
queue sweep then reads; the top-level classify ASKS for one about a failed merge-group
run. Two asks, two verbs, one word each way round.

every verb that runs git or gh also takes [--timeout <seconds>], default 120.

batch IS THE LANDING GATE AND IT PUSHES NOTHING. It clones --repo under --root, merges
each --pr head onto --base in the order given on a branch rowan/<name>, DROPS a head that
will not merge and says so, and then builds, vets, tests and runs the lisp suite over
what is left, one progress line per step on stderr with the elapsed time. Green is
"BATCH OK name=<name> base=<sha> head=<sha> members=<list> dropped=<list> skipped=<list>"
at exit 0, and red is the same line as BATCH FAIL naming the step, the failing packages and
the failing tests at exit 1. skipped= NAMES EVERY STEP THAT DID NOT RUN, so a green line
never claims a suite it only ran part of; --require-lisp turns a skipped lisp step into a
FAIL for a caller who needs it run, and a program that is not on PATH is also looked for
under ~/sdk/<toolchain>/bin before the step is skipped. The toolchain is checked against
the tree's go.mod BEFORE the first merge, so an old go on PATH is one refusal with the
remedy rather than a red build step quoting a download notice. checks=required is the
default: a member whose own head has no green ci-ok is DROPPED BEFORE THE MERGE, because
the gate runs on one operating system and CI runs on three and a member nobody has judged
on its own would turn the whole batch red for its own fault. A member whose head is a
batch's own branch (rowan/integration-*) or is named by a BATCH OK line in --receipt-file
is admitted on the gate's own evidence instead -- the same receipt nova-merge land takes.
--no-require-checks waives the whole check and says so on the verdict line. Pushing that
branch and opening the pull request is the caller's, who is
the one who knows whether this is the batch they wanted. --base defaults to dev, which is
where this repository's integration batches land; --root is rebuilt on every run, so give
it a directory of the batch's own.

THE GATE TESTS THE WAY CI TESTS. Its test step is the command .github/workflows/ci.yml
runs -- go test -json -count=1 ./... -- over the whole merged tree, and its verdict
is read from that -json stream by the same decoder cmd/nova-ci reads CI's with, so a batch
that goes green here is a batch that ran what CI runs. integration-4 went green under a
plain "go test ./..." and three CI legs then failed. The one thing not mirrored is CI's
fair share of the machine, which is the machine's own fact and not a number this tool may
write down: pass --gomaxprocs <n> on a bench that is also running CI, and the gate takes
that many cores instead of all of them.

land IS THE ONE ENTRANCE TO THE MERGE QUEUE (Glenn, 2026-09-18: nothing reaches the dev
merge queue but a batch). It reads the pull request back from the forge and enqueues it AT
THE FRONT -- through the enqueuePullRequest mutation, never a pr merge call and never
auto-merge -- after three refusals: a pull request that is not open, a pull request whose
own checks are not green, and a head that is not a batch's. A head is a batch's when its
branch is rowan/integration-*, the shape batch builds, or when --receipt carries that
head's own BATCH OK line; --receipt-file reads it from a file, taking the LAST line, so a
caller may hand it the gate's whole output. Everything else -- a card's branch, a green
swarm result, a revert -- is a member of a batch somebody has yet to build, and this verb
says so and stops.

queue audit is the other half of that lock: it lists every open pull request carrying
GitHub's auto-merge and TAKES IT OFF, because auto-merge is not an enqueue -- it is a
standing instruction the forge executes later with nobody in the room. Twenty-seven of them
were standing on 2026-09-18 and four had already landed themselves. --dry-run lists and
writes nothing.

simulate is the exception to the exit codes below: it exits 2 when it FINDS a poison
entry -- the one that is green alone and red on top of the entries ahead of it -- and 1
when it could not run at all, because a tool that could not run is not a red queue.

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

INIT AND QUICKSTART PUSH. init creates the lane's record branch -- the name given
to --lane-branch -- and, when the repository does not have it already, pushes it to
origin of the repository --repo names: one commit holding .gitignore, author and
committer nova-merge <nova-merge@localhost>, which is this tool's PLACEHOLDER
identity and not a person or an account anywhere. That branch is the transport for
every read and gate, so a lane is not usable without it; joined=false on INIT OK
says this lane created and pushed it, joined=true says it was already there and
nothing was pushed.

REHEARSE FIRST, against a bare repository of your own:

  git init -q --bare ./rehearsal.git
  nova-merge quickstart --lane ./rehearsal-lane --repo rehearsal-team/rehearsal --base main \
             --lane-branch nova-merge/main --remote "$PWD/rehearsal.git"

--remote only controls where git clones from and pushes to: the clone and the push
writes go to the local bare remote you name with --remote, but hosted status and
check reads can still occur against the repository named by --repo. --remote wants
an ABSOLUTE path or a URL: git runs inside the lane directory, so a relative one
resolves against the lane and the run is refused. Give the rehearsal a lane of its own --
init creates a lane once, so rehearsing into the live lane's directory is INIT
REFUSED on the line after. A bare repository with no --base branch in it prints one
STATUS NOTE and base_state=UNKNOWN at exit 0, which is a rehearsal with no base to
read rather than a failure. The two transcripts are in docs/TESTS.md, and both are run
by this binary's tests.

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
  nova-merge quickstart --lane ./rehearsal-lane --repo rehearsal-team/rehearsal --base main --lane-branch nova-merge/main --remote "$PWD/rehearsal.git"
  nova-merge add --lane ./rehearsal-lane --pr 949 --needs-read
  nova-merge status --lane ./rehearsal-lane
  nova-merge packet --lane ./rehearsal-lane --who emma --all
  nova-merge dry-run --lane ./rehearsal-lane

Those five are one sitting against a bare repository of your own, in order: make the
lane, queue an entry, look at it, ask what a
reader would be handed, and see what a pass would do without doing it. ./rehearsal-lane
is a path of yours and nothing is guessed from it.
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
	// NewSweepHost is `sweep`'s forge: a repository's merge queue, its open pull
	// requests and their runs. It is a separate seam from NewHost because it is a
	// separate verb with a separate host interface, and the test that sweeps a fake
	// queue must not have to stand up a lane's host to do it.
	NewSweepHost func(repo, branch string, timeout time.Duration) merge.SweepHost
	// NewEnqueueHost is THE ONE EDGE ONTO A MERGE QUEUE'S WRITE SIDE, and `land` is its
	// one caller. It is a seam of its own rather than a method on the lane's host for the
	// reason the lock exists at all: the smaller the surface that can enqueue, the fewer
	// the ways something reaches the queue that nobody meant to put there.
	NewEnqueueHost func(repo string, timeout time.Duration) merge.EnqueueHost
	// NewAuditHost is `queue audit`'s edge: the open pull requests carrying an auto-merge,
	// and the one call that takes it off.
	NewAuditHost func(repo string, timeout time.Duration) merge.AuditHost
	// NewQueue reads the live merge queue for `simulate --entries`-less runs. It is a
	// field so the tests hand it a fake queue and reach no network.
	NewQueue func(repo string, timeout time.Duration) QueueReader
	// NewRebaseList is the gh seam the rebase verb reads the open list through; the
	// production one is the same GH the merge pass uses, which implements both edges.
	NewRebaseList func(repo string, timeout time.Duration) merge.RebaseList
	// Launcher starts one rebase card on a bench. The tests inject a fake.
	Launcher merge.Launcher
	Runner   merge.Runner
	// BatchGate is the suite `batch` runs, and a nil one -- which is what production()
	// leaves it -- is the whole of batchGate. It is injected for ONE reason, written out
	// at crossVetStep: the cross vet has to build the windows standard library before it
	// can type-check anything, and a unit test that pays that inside `go test` spends the
	// package's own -timeout on it and starves every parallel test behind it.
	BatchGate []batchStep
	BuildID   func() string
	// Dial and Forge are the react verb's two edges: the pub/sub instance it subscribes
	// to, and the forge it asks which PRs a base move made DIRTY. They are injected so a
	// test drives a miniredis and a fake forge and reaches no network.
	Dial  func(addr string) *redis.Client
	Forge func(repo, base string, timeout time.Duration) ci.Forge
}

func production() Deps {
	return Deps{
		Now:     func() time.Time { return time.Now().UTC() },
		Sleep:   time.Sleep,
		RepoURL: func(repo string) string { return "https://github.com/" + repo + ".git" },
		NewHost: func(repo string, timeout time.Duration) merge.Host {
			return merge.NewGH(repo, timeout, nil)
		},
		NewSweepHost: func(repo, branch string, timeout time.Duration) merge.SweepHost {
			return merge.NewGHSweep(repo, branch, timeout, nil)
		},
		NewEnqueueHost: func(repo string, timeout time.Duration) merge.EnqueueHost {
			return merge.NewGHEnqueue(repo, timeout, nil)
		},
		NewAuditHost: func(repo string, timeout time.Duration) merge.AuditHost {
			return merge.NewGHEnqueue(repo, timeout, nil)
		},
		NewQueue: func(repo string, timeout time.Duration) QueueReader {
			return newGHQueue(repo, timeout, nil)
		},
		NewRebaseList: func(repo string, timeout time.Duration) merge.RebaseList {
			return merge.NewGH(repo, timeout, nil)
		},
		Launcher: merge.BenchLauncher{},
		BuildID:  buildID,
		Dial:     func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(repo, base string, timeout time.Duration) ci.Forge {
			return ci.NewGHForge(repo, base, timeout)
		},
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
	// `nova-merge <verb> --help` is a question, not a parse failure. These verbs
	// share one flag-parsing helper that answers over stderr and has no way to
	// say "answered, exit 0", so the question is answered here, before any flag
	// set exists; internal/cliflags says why that is the shape.
	if cliflags.Answer(stdout, usage, args) {
		return 0
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr, deps)
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
	case "queue":
		return cmdQueue(rest, stdout, stderr, deps)
	case "classify":
		return cmdClassify(rest, stdout, stderr, deps)
	case "wait":
		return cmdWait(rest, stdout, stderr, deps)
	case "sweep":
		return cmdSweep(rest, stdout, stderr, deps)
	case "simulate":
		return cmdSimulate(rest, stdout, stderr, deps)
	case "rebase":
		return cmdRebase(rest, stdout, stderr, deps)
	case "react":
		return cmdReact(rest, stdout, stderr, deps)
	case "batch":
		return cmdBatch(rest, stdout, stderr, deps)
	case "land":
		return cmdLand(rest, stdout, stderr, deps)
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
	// `wait` watches one pull request by polling the host, `sweep` reads a repository's
	// merge queue, `simulate` names the repository it makes a scratch worktree from,
	// `batch` names the one it clones, and `rebase` is not a lane verb at all -- it reads
	// the open list from a repository and cuts cards into a directory -- so all five name
	// the repository outright rather than reading it from the lane's state, like `init`
	// does; every other verb reads the lane's.
	namesRepo := verb == "wait" || verb == "sweep" || verb == "simulate" || verb == "rebase" || verb == "batch" || verb == "land" || verb == "queue"
	for _, name := range []string{"repo", "lane-branch", "remote"} {
		if name == "repo" && namesRepo {
			continue
		}
		if !creation && has(name) {
			return refuse(stderr, " "+verb, fmt.Sprintf("--%s belongs to `init`, which writes it into the lane once; every other verb reads it from the lane's state", name)), true
		}
	}
	if !creation && has("base") {
		switch verb {
		case "gate":
			return refuse(stderr, " gate", "--base is the lane's branch and belongs to `init`; the base SHA a gate was taken against is --base-sha, a different word on purpose"), true
		case "simulate", "batch":
			// simulate predicts a queue onto a base branch and batch builds an
			// integration branch on top of one; neither owns a lane's.
		case "rebase":
			// rebase cuts a card per open pull request against a base branch it names
			// outright; it is not a lane verb, so it does not own a lane's --base either.
		default:
			return refuse(stderr, " "+verb, "--base belongs to `init`, which writes it into the lane once; a --base here would let two invocations disagree about where the lane lands"), true
		}
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
//
// It takes a finished sentence rather than a format and its arguments, so that every
// value that reaches stderr is classified at the site that knows what it is -- which is
// what internal/oneline/audit walks, and which a format string passed through a helper
// would hide.
func (f *flags) problem(text string) {
	f.problems = append(f.problems, text)
}

func (f *flags) parse(args []string, stderr io.Writer) bool {
	if err := f.fs.Parse(args); err != nil {
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		f.problem(fmt.Sprintf("takes no positional arguments, got %d (flags come before arguments)", n))
	}
	return true
}

// require is the no-guessing law, and it says what the flag WANTS rather than only that
// it is missing.
func (f *flags) require(name, value, wants string) {
	if strings.TrimSpace(value) == "" {
		f.problem(fmt.Sprintf("--%s is required; refusing to guess: %s", name, wants))
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
	// A DEADLINE PINNED FROM ONE SIDE ONLY IS NOT PINNED. An hour is longer than any
	// fetch, clone or gh call this tool makes; past it, a tool that is "waiting" and a
	// tool that has stopped saying anything are the same thing to whoever is reading.
	if *l.timeout < 1 || *l.timeout > maxTimeout {
		l.problem(fmt.Sprintf("--timeout is a number of seconds this tool waits for git or gh before saying so, from 1 to %d, got %d", maxTimeout, *l.timeout))
	}
	if *l.max < 0 {
		l.problem(fmt.Sprintf("--max is a ceiling on a listing: 0 means all and a negative one is a typo with two readings, got %d", *l.max))
	}
}

// maxTimeout, maxHours and minLoop are the far sides of this tool's three deadlines. Each
// is stated rather than left open, because a bound with no ceiling is a bound nobody set
// (lessons 75, 76, 172).
const (
	maxTimeout = 3600 // seconds: longer than any git or gh call this tool makes
	maxHours   = 24   // a loop nobody outlives is a loop nobody notices has stuck
	minLoop    = time.Second
)

func (l *laneFlags) dur() time.Duration { return time.Duration(*l.timeout) * time.Second }

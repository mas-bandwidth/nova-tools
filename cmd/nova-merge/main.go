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
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-merge: the typed read, the gate record and the batch gate a stream lands on (see docs/SPEC-MERGE.md)

usage:
  nova-merge version    print this build identity (--version also accepted)
  nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --verdict approve|hold [--note <text>] [--redis <addr>]
  nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --base-sha <sha> --merge <sha> --verdict green|red --summary <path>
  nova-merge fold       --branches <file> --onto <base> --out <branch> [--lane <dir>]
  nova-merge fold       --close-folded --pr <n>
  nova-merge classify   --lane <dir> --run <id> [--base-url <url>] [--key-env <name>]
  nova-merge receipt    --repo <owner>/<name> --pr <n> [--timeout <seconds>]
  nova-merge batch      --name <name> --pr <list> --repo <owner>/<name> --root <dir> [--base <branch>] [--reference <mirror>] [--timeout <duration>] [--gomaxprocs <n>] [--require-lisp] [--no-require-checks] [--check-name <name>] [--receipt-file <path>] [--sibling <name>=<url>@<ref>]

every verb that runs git or gh also takes [--timeout <seconds>], default 120.

The per-PR lander role is retired (stream is the unit): init, quickstart, add,
add-branch, run, status, dry-run, packet, stop, queue, wait, sweep, simulate, rebase,
react, land, integrate and stack are gone. What stays is the evidence a stream
lands on: read records a typed verdict at a head, gate records a local gate's verdict
for a merge, classify asks one typed question about a failed merge-group run, batch
merges N heads onto a base and runs the tests, and fold folds branches onto a base into
one out-branch. Landing a stream onto dev is by hand until the stream-lander spec.

batch IS THE LANDING GATE AND IT PUSHES NOTHING. It clones --repo under --root, merges
each --pr head onto --base in the order given on a branch rowan/<name>, DROPS a head that
will not merge and says so, and then builds, vets, tests and runs the lisp suite over
what is left, one progress line per step on stderr with the elapsed time. Green is
"BATCH OK name=<name> base=<sha> head=<sha> members=<list> dropped=<list> skipped=<list> checks=<required|waived> [check=<name>]"
at exit 0, and red is the same line as BATCH FAIL naming the step, the failing packages,
the failing tests and the step's captured stderr (capped at oneline.TailBytes) at exit 1. skipped= NAMES EVERY STEP THAT DID NOT RUN, so a green line
never claims a suite it only ran part of; --require-lisp turns a skipped lisp step into a
FAIL for a caller who needs it run, and a program that is not on PATH is also looked for
under ~/sdk/<toolchain>/bin before the step is skipped. The toolchain is checked against
the tree's go.mod BEFORE the first merge, so an old go on PATH is one refusal with the
remedy rather than a red build step quoting a download notice. checks=required is the
default: a member whose own head has no green required check is DROPPED BEFORE THE MERGE, because
the gate runs on one operating system and CI runs on three and a member nobody has judged
on its own would turn the whole batch red for its own fault. The check's name is ci-ok
here, --check-name or .nova-merge required-check= elsewhere, and it is printed as check=
on BATCH OK and BATCH DROP so a lane script can parse it. A member whose head is a
batch's own branch (rowan/integration-*) or is named by a BATCH OK line in --receipt-file
is admitted on the gate's own evidence instead.
--no-require-checks waives the whole check and says so on the verdict line. Pushing that
branch and opening the pull request is the caller's, who is
the one who knows whether this is the batch they wanted. --base defaults to dev, which is
where this repository's integration batches land; --root is rebuilt on every run, so give
it a directory of the batch's own. --sibling <name>=<url>@<ref> (repeatable) clones that
repository beside the job checkout (repo/) at the named branch or tag, so a tree whose
tests look next door — schema's serialize.go at ../serialize.go — finds it after the
rebuild. name is one path element (dots allowed); repo and tmp are reserved. The last @
splits url from ref.

THE GATE TESTS THE WAY CI TESTS. Its test step is the command .github/workflows/ci.yml
runs -- go test -json -count=1 ./... -- over the whole merged tree, and its verdict
is read from that -json stream by the same decoder cmd/nova-ci reads CI's with, so a batch
that goes green here is a batch that ran what CI runs. The test step writes that whole
stream to <root>/test-<round>.jsonl before it is condensed. Round is 1 the first time
that root keeps one and the next free integer after that, so a re-run does not erase the
stream a red left behind; the directory rebuilt each run is <root>/<name>, and the stream
is not inside it. A red test step's reason begins with stream=<that path>. integration-4
went green under a plain "go test ./..." and three CI legs then failed. The one thing not
mirrored is CI's fair share of the machine, which is the machine's own fact and not a
number this tool may write down: pass --gomaxprocs <n> on a bench that is also running
CI, and the gate takes that many cores instead of all of them.

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a red batch, a fold
that could not be made, a publication the remote refused; 2 could not run: missing flag,
unreadable lane state, a directory that is not a lane, bad invocation, git or gh absent.

No guessed anything. There is no default lane directory, no default repository and
no default base; a missing one is exit 2 and refusing to guess. --timeout defaults to
120 seconds, because a subprocess timeout is how long this tool waits before saying so
rather than a fact about a lane that only its owner can supply.

The repository, the base and the lane branch are properties of the LANE, written once
when the lane was made; read and gate read them from there. batch names --repo and
--base because it reaches the forge without a lane. The flag on gate that names the
base SHA is spelled --base-sha so the two are never one word.

A read and a gate are each ONE IMMUTABLE FILE in the lane's branch, written to a
durable outbox first and pushed by the tool in a compare-and-swap loop, so a reader
on another machine records a verdict where every lane on that branch will fold it.
state.json is the fold and the files are the truth: losing it loses the order and
nothing else.

example:
  nova-merge version
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
	Dial func(addr string) *redis.Client
	// TestTree runs the package test the repository names for the fold's scratch tree
	// (docs/SPEC-MERGE.md "The fold (#1142)"): lisp/nova-work/run-tests.sh for a
	// nova-work fold, go test for Go. TestLayout runs the layout test of #560 after it.
	// Both are injected so a fold test uses a fake and reaches no toolchain.
	TestTree   func(dir string) error
	TestLayout func(dir string) error
}

func production() Deps {
	return Deps{
		Now:     func() time.Time { return time.Now().UTC() },
		Sleep:   time.Sleep,
		RepoURL: func(repo string) string { return "https://github.com/" + repo + ".git" },
		NewHost: func(repo string, timeout time.Duration) merge.Host {
			return merge.NewGH(repo, timeout, nil)
		},
		BuildID:    buildID,
		Dial:       func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		TestTree:   realTestTree,
		TestLayout: realTestLayout,
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
		return refuse(stderr, "", "no verb given; run: nova-merge help")
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
	case "read":
		return cmdRead(rest, stdout, stderr, deps)
	case "gate":
		return cmdGate(rest, stdout, stderr, deps)
	case "fold":
		return cmdFold(rest, stdout, stderr, deps)
	case "classify":
		return cmdClassify(rest, stdout, stderr, deps)
	case "receipt":
		return cmdReceipt(rest, stdout, stderr, deps)
	case "batch":
		return cmdBatch(rest, stdout, stderr, deps)
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
	// `batch` names the repository it clones outright; the lane verbs (read, gate,
	// classify) read the repository, the base and the lane branch from the lane's state,
	// written once when the lane was made.
	for _, name := range []string{"repo", "lane-branch", "remote"} {
		if name == "repo" && (verb == "batch" || verb == "receipt") {
			continue
		}
		if has(name) {
			return refuse(stderr, " "+verb, fmt.Sprintf("--%s is the lane's, written once when the lane was made; this verb reads it from the lane's state", name)), true
		}
	}
	if has("base") {
		switch verb {
		case "gate":
			return refuse(stderr, " gate", "--base is the lane's branch; the base SHA a gate was taken against is --base-sha, a different word on purpose"), true
		case "batch":
			// batch builds an integration branch on top of a base it names; it owns no
			// lane's.
		default:
			return refuse(stderr, " "+verb, "--base is the lane's, written once when the lane was made; a --base here would let two invocations disagree about where the lane lands"), true
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

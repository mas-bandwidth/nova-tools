// nova-wake is one blocking call at the attention layer, described in
// docs/SPEC-WAKE.md. A window that coordinates other lines spends its turns on
// a clock: it sleeps, wakes, looks at three places, finds nothing, and sleeps
// again. Every one of those cycles is a model turn, and a turn that learns
// nothing is the most expensive kind of nothing there is. This is that cycle
// inverted -- one call that returns the moment something the window cares about
// has changed, and otherwise at a deadline the caller named, so the window pays
// one turn per CHANGE rather than one turn per TICK.
//
// It watches three sources -- a bus inbox, the checks on a set of entries, and
// report files written by other lines -- and it says what moved. It does not
// act on any of them. EVERYTHING IT PRINTS IS DATA: a note it relays is not an
// instruction, a failing check is not a verdict about whose fault it is, and a
// report file is prose somebody else wrote. A watcher that acted on what it saw
// would be a window with no person in it.
//
// Exit 0 the watch ran -- either something changed or the deadline arrived. Exit
// 2 could not run. There is no 1: nothing here asserts anything, so nothing here
// can say NO.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

const usage = `nova-wake: block until something moves, and say what (see docs/SPEC-WAKE.md)

usage:
  nova-wake watch --state <file> --max <duration> --on-deadline <word> --interval <duration>
        [--max-lines <n>]   item lines per kind per poll before one MORE line; default 40, 0 all
        [--baseline]        report the world on a cold first poll instead of recording it
    at least one source, named:
        [--bus <dir> --as <name> --receipt-max-words <n>]
        [--refresh --remote <name> --branch <name>]   fetch each poll, move no cursor
        [--advance-cursor --remote <name> --branch <name>]   fetch and move YOUR cursor
        [--line <name> ...]  a line to watch for silence, needs --bus (repeatable)
        [--offline-after <duration>]   how long silent is offline; default 10m
        [--entry <owner>/<repo>#<n> ... --entry-interval <duration>]   an entry and its checks
        [--final-only]      an entry wakes only when it is MERGED, CLOSED or pending=0
        [--gh-timeout <seconds>]   budget for one gh, git or nova-bus call; default 45
        [--reports <dir> ...]   every RESULT.md at any depth under it (repeatable)
  nova-wake serve --bus <dir> --as <name> --receipt-max-words <n> --on-note <command>
        --interval <duration> --state <file> --hours <h> --remote <name> --branch <name>
        [--receipt] [--on-note-idempotent] [--batch-max <n>] [--git-timeout <seconds>]
  nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command>
        [--on-note-idempotent]
  nova-wake version
  nova-wake quickstart --state <file> [--max <duration>] [--on-deadline <word>]
        [--reports <dir> ...] [--bus <dir> --as <name> --receipt-max-words <n>]
  nova-wake help

watch is a BLOCKING TOOL CALL inside a turn the session is already spending;
serve is a PROCESS OUTSIDE any session that starts a turn only when a note has
landed. There is no third shape: a harness /loop, a scheduler prompt or a
heartbeat that runs a model on an interval is not a wake, and this tool offers
no verb for it.

serve FETCHES every --interval, which is why --remote and --branch are its own
flags and not --receipt's, and its --on-note command is started as
<command> <id> [<id>...]: note ids and nothing else, no stdin, no environment,
its output discarded. Its last line is WAKE SERVE, and a note whose command
exited non-zero is uncertain and waits for a person's --redeliver, counted
failed= there.

There are no defaults for --state, --bus, --as, --entry, --reports, --interval,
--max, --receipt-max-words or --on-deadline: each missing one is a refusal,
refusing to guess. --interval and --entry-interval have a 5s floor and --max a
60m ceiling.
--max-lines (40), --gh-timeout (45s) and --offline-after (10m) have defaults,
because none of them is a fact about your world that only you can supply.

exit codes: 0 the watch ran (something changed, or the deadline arrived);
2 could not run. Read the SECOND TOKEN OF THE LAST LINE -- WAKE CHANGE, WAKE
QUIET or WAKE BROKEN -- and never the exit code, to learn which it was.

example:
  nova-wake quickstart --state ./wake.state --reports ./reports
  nova-wake watch --state ./wake.state --max 5s --on-deadline report --interval 5s --reports ./reports
  nova-wake watch --state ./wake.state --max 5s --on-deadline hold --interval 5s --reports ./reports --max-lines 0 --baseline

./reports there is a directory of your own holding RESULT.md files, and
./wake.state is a path this tool may write; cmd/nova-wake/testdata/example-reports
in this repo is a fixture the size of a first run, and every line above is run
against it by the tests.
`

// The hints. The no-guessing law is unchanged -- a missing flag is exit 2 and
// says refusing to guess -- but a refusal naming only the fault has moved the
// guessing onto the reader.
const (
	stateHint      = `--state <file> is this tool's own file, one per watch: it holds what each watched thing last looked like and what you have already been shown, and deleting it is a cold start rather than a loss`
	maxHint        = `--max <duration> is when to give up and return, and it is the one thing a caller must state: a watcher with no deadline is a window that is stuck rather than waiting, and nobody outside can tell the two apart. Ask your harness what its tool-call limit is and sit under it; 20m is the recommendation and 60m the ceiling`
	deadlineHint   = `--on-deadline <word> is what YOU will do if nothing moves, echoed back on the verdict so the transcript records the decision. This tool takes no action itself: the word is yours (report, hold, merge, ask)`
	intervalHint   = `--interval <duration> is the cadence for the bus, --line and the report directories, and it has no default because the right cadence is a fact about the watched thing's rate that only you know; it will not go below 5s, because a poll is a git fetch and a call against somebody else's server`
	entryEveryHint = `--entry-interval <duration> is the expected length of the hosted run: an 8-minute CI run deserves one check at 8 minutes, not eight checks at one minute`
	sourceHint     = `name at least one source: --bus <dir> --as <name> --receipt-max-words <n>, --entry <owner>/<repo>#<n>, or --reports <dir>. A watch with nothing to watch is a sleep with a longer name`
	asHint         = `--as <name> is the name YOU read the bus as, spelled the way the bus's roster spells it; it is the claim this tool makes on your behalf and there is no flag that reads as somebody else`
	busHint        = `--bus <dir> is the bus checkout to read, a git clone of the shared notes repository`
	wordsHint      = `--receipt-max-words <n> is handed to nova-bus and decides how much of a receipt it prints; nova-bus has no default for it and neither has this`
	remoteHint     = `--refresh fetches through nova-bus wait, which needs --remote <name> and --branch <name> -- the same two you would hand nova-bus yourself`
	onNoteHint     = `--on-note <command> is the command a landed note starts, and it receives note ids and nothing else; what it is (a claude -p, an opencode run, a grok invocation) is your business and never this tool's`
	hoursHint      = `--hours <h> is how long this serve runs before it ends on its own, as a DECIMAL number of hours rather than a whole one: 8 is a working day, 0.5 is thirty minutes and 0.02 is about a minute, which is how a first run tries it. A process with no end is the orphaned shell of 2026-09-09`
	redeliverHint  = `--redeliver <id> runs an uncertain dispatch once more, and it is a person's act: you have ended the earlier command or watched it return. It needs --on-note, because the state stores no command`
)

func hintFor(name string) string {
	switch name {
	case "state":
		return "  " + stateHint + "\n"
	case "max":
		return "  " + maxHint + "\n"
	case "on-deadline":
		return "  " + deadlineHint + "\n"
	case "interval":
		return "  " + intervalHint + "\n"
	case "entry-interval":
		return "  " + entryEveryHint + "\n"
	case "as":
		return "  " + asHint + "\n"
	case "bus":
		return "  " + busHint + "\n"
	case "receipt-max-words":
		return "  " + wordsHint + "\n"
	case "on-note":
		return "  " + onNoteHint + "\n"
	case "hours":
		return "  " + hoursHint + "\n"
	}
	return ""
}

// MaxCeiling is the harness's, not this tool's. A watch runs inside a tool call
// and every harness kills a call that runs too long, so a --max above the
// harness's limit does not watch longer -- it is killed with nothing said at
// all, which loses both the changes seen and the state not yet written.
const MaxCeiling = 60 * time.Minute

// IntervalFloor: a poll is a git fetch and an API call against somebody else's
// server.
const IntervalFloor = 5 * time.Second

// DefaultMaxLines is 40 rather than this repo's 20, and the reason is that a
// wake line is not a listing: it is the whole of what the window learns from
// this return.
const DefaultMaxLines = 40

// DefaultGHTimeout is the budget for one gh, git or nova-bus call.
const DefaultGHTimeout = 45

// Version is this build, printed by `nova-wake version` and read by the nova-bus
// pin: one tag ships nova-wake and nova-bus together, so the version of this
// build IS the nova-bus version this build is written against (internal/wake's
// AcceptBus). It was a hand-written literal until 2026-09-12, when a release the
// literal could not follow locked nova-wake out of its own nova-bus.
func Version() string { return buildVersion() }

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, stdout, stderr, wake.Real{})
}

func runWith(args []string, stdout, stderr io.Writer, clock wake.Clock) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; watch is the blocking call, serve is the process outside a session")
	}
	switch args[0] {
	case "watch":
		return cmdWatch(args[1:], stdout, stderr, clock, false)
	case "quickstart":
		return cmdWatch(args[1:], stdout, stderr, clock, true)
	case "serve":
		return cmdServe(args[1:], stdout, stderr, clock)
	case "version":
		// The first question after a table misbehaves is which build each line
		// is running, and a tool that cannot answer it costs a person the
		// asking (lesson 142).
		fmt.Fprintf(stdout, "nova-wake %s %s/%s %s\n", oneline.Field(Version()),
			oneline.Field(runtime.GOOS), oneline.Field(runtime.GOARCH), oneline.Field(runtime.Version()))
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// refuse is what an unusable invocation costs: ONE line naming what was wrong
// and the door to the usage, never the 60-line banner. A flag typo used to cost
// the whole of it, on every typo, and a harness reading a tool's stderr pays
// that every time.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-wake%s: %s; run: nova-wake help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// refused prints the WAKE REFUSED line, which is the refusal shape for the
// things that are wrong about the WORLD rather than about the invocation: a
// second watcher on one state file, a nova-bus of the wrong version, a flag
// this build does not carry.
func refused(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "WAKE REFUSED: %s\n", oneline.Escape(what))
	return 2
}

// repeated is a flag that may be given more than once: --entry, --reports,
// --line. A comma-separated list would make a name holding a comma
// unspellable, and the prototype's --prs 942,951 is exactly the shape this
// replaces.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// problems collects every independent complaint so that ONE run names them all.
// Sending a first run back three times for three independent flags is three
// refusals the first one already knew about.
type problems struct {
	list []string
	hint []string
}

func (p *problems) add(what, hint string) {
	p.list = append(p.list, what)
	p.hint = append(p.hint, hint)
}

func (p *problems) missing(flag string) {
	p.add("--"+flag+" is required; refusing to guess", hintFor(flag))
}

func (p *problems) print(stderr io.Writer, verb string) int {
	for i, what := range p.list {
		fmt.Fprintf(stderr, "nova-wake %s: %s; run: nova-wake help\n", oneline.Escape(verb), oneline.Escape(what))
		// The hint is one INDENTED line under the refusal it belongs to, and
		// it goes through the escape like everything else: most of these are
		// package constants, but the ones a flag's own value reaches (a
		// duration that did not parse, an entry that is not an entry) are not.
		if h := strings.TrimSpace(p.hint[i]); h != "" {
			fmt.Fprintf(stderr, "  %s\n", oneline.Escape(h))
		}
	}
	return 2
}

func (p *problems) any() bool { return len(p.list) > 0 }

// sourceName is the fail:<source> key and the word on WAKE POLL and WAKE
// BROKEN.
type polled struct {
	src wake.Source
	due time.Time
}

func cmdWatch(args []string, stdout, stderr io.Writer, clock wake.Clock, quickstart bool) int {
	verb := "watch"
	if quickstart {
		verb = "quickstart"
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	var (
		state        = fs.String("state", "", "")
		maxDur       = fs.String("max", "", "")
		onDeadline   = fs.String("on-deadline", "", "")
		interval     = fs.String("interval", "", "")
		maxLines     = fs.Int("max-lines", DefaultMaxLines, "")
		baseline     = fs.Bool("baseline", false, "")
		busDir       = fs.String("bus", "", "")
		as           = fs.String("as", "", "")
		words        = fs.Int("receipt-max-words", 0, "")
		refresh      = fs.Bool("refresh", false, "")
		remote       = fs.String("remote", "", "")
		branch       = fs.String("branch", "", "")
		advance      = fs.Bool("advance-cursor", false, "")
		offlineAfter = fs.String("offline-after", wake.Dur(wake.DefaultOfflineAfter), "")
		entryEvery   = fs.String("entry-interval", "", "")
		finalOnly    = fs.Bool("final-only", false, "")
		ghTimeout    = fs.Int("gh-timeout", DefaultGHTimeout, "")
		entries      repeated
		reports      repeated
		lines        repeated
	)
	fs.Var(&entries, "entry", "")
	fs.Var(&reports, "reports", "")
	fs.Var(&lines, "line", "")
	if !parseFlags(fs, args, stderr) {
		return 2
	}

	var p problems
	if *state == "" {
		p.missing("state")
	}
	if quickstart {
		if *maxDur == "" {
			*maxDur = "5s"
		}
		if *onDeadline == "" {
			*onDeadline = "report"
		}
		*interval = wake.Dur(IntervalFloor)
		*baseline = true
	}
	if *maxDur == "" {
		p.missing("max")
	}
	if *onDeadline == "" {
		p.missing("on-deadline")
	}
	if *interval == "" {
		p.missing("interval")
	}
	max := parseDur(&p, "max", *maxDur)
	every := parseDur(&p, "interval", *interval)
	offline := parseDur(&p, "offline-after", *offlineAfter)
	if max > MaxCeiling {
		p.add("--max "+*maxDur+" is over the "+wake.Dur(MaxCeiling)+" ceiling", "  a watch runs inside a tool call, and a --max above your harness's limit does not watch longer: it is killed with nothing said at all, losing both the changes seen and the state not yet written. Ask your harness what its limit is and sit under it\n")
	}
	if every > 0 && every < IntervalFloor {
		p.add("--interval "+*interval+" is below the "+wake.Dur(IntervalFloor)+" floor", "  a poll is a git fetch and a call against somebody else's server; 5s is the fastest this tool will ask for one\n")
	}
	if *maxLines < 0 {
		p.add("--max-lines is negative", "  0 already means all, so a negative number is a typo with two readings; --max-lines <n> is how many item lines of one kind print before one MORE line stands for the rest\n")
	}
	if *ghTimeout <= 0 {
		p.add("--gh-timeout must be a positive number of seconds", "  a budget of zero or less is not 'unlimited'; it is a call that can never finish\n")
	}
	if len(entries) > 0 && *entryEvery == "" {
		p.missing("entry-interval")
	}
	var entryEveryDur time.Duration
	if *entryEvery != "" {
		entryEveryDur = parseDur(&p, "entry-interval", *entryEvery)
		if entryEveryDur > 0 && entryEveryDur < IntervalFloor {
			p.add("--entry-interval "+*entryEvery+" is below the "+wake.Dur(IntervalFloor)+" floor", "")
		}
	}
	for _, name := range entries {
		if _, _, err := wake.SplitEntry(name); err != nil {
			p.add("--entry "+name+" is not an entry", "  "+oneline.Err(err)+"\n")
		}
	}
	if *busDir != "" {
		if *as == "" {
			p.missing("as")
		}
		if *words <= 0 {
			p.missing("receipt-max-words")
		}
	}
	if *busDir == "" {
		if *as != "" || *refresh || *advance {
			p.missing("bus")
		}
		if len(lines) > 0 {
			p.add("--line needs --bus; refusing to guess", "  a line's last sign is a commit on the bus checkout's branch, so there is nothing to read it from without one\n")
		}
	}
	if *refresh && *advance {
		p.add("--refresh and --advance-cursor together is one fetch too many", "  --refresh fetches and moves no cursor; --advance-cursor fetches inside the push that moves it. One fetch per poll, never two\n")
	}
	if *refresh && (*remote == "" || *branch == "") {
		p.add("--refresh needs --remote and --branch", "  "+remoteHint+"\n")
	}
	if len(entries) == 0 && len(reports) == 0 && *busDir == "" {
		p.add("no source named; refusing to guess", "  "+sourceHint+"\n")
	}
	if p.any() {
		return p.print(stderr, verb)
	}
	if *advance {
		if *as == "" {
			return refused(stderr, "--advance-cursor without --as: the --as name IS the claim, and a watcher may not advance a cursor that is not the window's own")
		}
		if *remote == "" || *branch == "" {
			return refused(stderr, "--advance-cursor without --remote and --branch: the advance is the one write-side call this tool makes, and it pushes")
		}
		// One advancing watcher per (bus, as), for the WHOLE call: two calls
		// over one pair interleave, each consuming the notes the other should
		// have relayed, and each returns a partial listing that looks complete.
		releaseCursor, cursorHolder, cerr := wake.LockAdvance(*busDir, *as)
		if cerr != nil {
			return refused(stderr, oneline.Err(cerr))
		}
		if releaseCursor == nil {
			return refused(stderr, "another nova-wake is advancing "+*as+"'s cursor on this bus (pid "+cursorHolder+"); one advancing watcher per bus and name, because a cursor two runs move is a claim neither of them can make")
		}
		defer releaseCursor()
	}

	timeout := time.Duration(*ghTimeout) * time.Second

	// One writer per --state, for the whole call.
	release, holder, err := wake.LockState(*state)
	if err != nil {
		return refused(stderr, oneline.Err(err))
	}
	if release == nil {
		return refused(stderr, "another nova-wake holds "+wake.LockName(*state)+" (pid "+holder+"); two watches sharing one state file each write the whole map, so the later write erases what the earlier one learned. Give this watch a state file of its own")
	}
	defer release()

	st, err := wake.Load(*state)
	if err != nil {
		return refused(stderr, "the state file "+*state+" could not be read: "+oneline.Err(err)+
			"; repair it, or pass a new --state path and accept a cold start on purpose")
	}

	ctx := context.Background()
	busVersion := "-"
	var sources []*polled
	var busSrc *wake.Bus
	var lineView *wake.Lines
	now := clock.Now()

	if *busDir != "" {
		found, verr := wake.BusVersion(ctx, timeout)
		switch {
		case verr != nil:
			// A source whose program is missing from PATH is a CHANGE on the
			// first poll and not a refusal, because a window that cannot see
			// its bus needs to hear so now. Only a nova-bus that answers with
			// the wrong version is refused.
			busVersion = "-"
		case !wake.AcceptBus(Version(), found):
			return refused(stderr, wake.BusRefusal(Version(), found))
		default:
			busVersion = found
		}
		busSrc = &wake.Bus{
			Dir: *busDir, As: *as, ReceiptMaxWords: *words, Every_: every, Timeout: timeout,
			Refresh: *refresh, Remote: *remote, Branch: *branch,
			Seen:    func(line string) bool { _, ok := st.Get("bus:line:" + line); return ok },
			Printed: func(id string) bool { return st.PrintedID("bus:note:"+id) != "-" },
		}
		sources = append(sources, &polled{src: busSrc, due: now})
	}
	if len(entries) > 0 {
		sources = append(sources, &polled{src: &wake.Entries{
			Names: entries, Every_: entryEveryDur, Timeout: timeout, Final: *finalOnly,
		}, due: now})
	}
	if len(reports) > 0 {
		sources = append(sources, &polled{src: &wake.Reports{Dirs: reports, Every_: every}, due: now})
	}
	if len(lines) > 0 {
		lineView = &wake.Lines{Bus: *busDir, Names: lines, After: offline, Timeout: timeout, Start: now}
	}

	w := &watcher{
		stdout: stdout, stderr: stderr, clock: clock, st: st, statePath: *state,
		maxLines: *maxLines, finalOnly: *finalOnly, baseline: *baseline,
		cold: st.Cold() && !*baseline, sources: sources, bus: busSrc, lines: lineView,
		busDir: *busDir, timeout: timeout, every: every, lineDue: now,
	}
	if *advance {
		w.advancer = &wake.Advancer{Bus: busSrc, Clock: clock}
	}
	if quickstart {
		fmt.Fprintf(stdout, "WAKE NOTE quickstart chose --baseline, --interval %s and --max %s, so a first run returns with the world listed once rather than blocking; --on-deadline %s is the word it echoes back\n",
			oneline.Field(*interval), oneline.Field(*maxDur), oneline.Field(*onDeadline))
	}
	fmt.Fprintf(stdout, "WAKE at=%s as=%s max=%s interval=%s on-deadline=%s sources=%s state=%s cold=%t nova-bus=%s pending=%d\n",
		oneline.Field(wake.Stamp(now)), oneline.Field(dash(*as)), oneline.Field(wake.Dur(max)),
		oneline.Field(wake.Dur(every)), oneline.Field(*onDeadline), oneline.Field(sourceList(*busDir, entries, reports)),
		oneline.Field(*state), w.cold, oneline.Field(busVersion), st.Pending())
	if *busDir != "" && !*refresh && !*advance {
		w.note("bus checkout is read as it stands; nothing fetches without --advance-cursor; freshness is head-at=")
	}
	return w.loop(ctx, now, max, *onDeadline)
}

// sourceList is the opening line's sources= field: the sources this run was
// told to watch, in the grammar's order.
func sourceList(busDir string, entries, reports []string) string {
	var out []string
	if busDir != "" {
		out = append(out, "bus")
	}
	if len(entries) > 0 {
		out = append(out, "entries")
	}
	if len(reports) > 0 {
		out = append(out, "reports")
	}
	return strings.Join(out, ",")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func parseDur(p *problems, name, v string) time.Duration {
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.add("--"+name+" "+v+" is not a duration", "  a duration is a number and a unit, as in 20m, 45s or 2h; "+hintText(name))
		return 0
	}
	if d <= 0 {
		p.add("--"+name+" "+v+" is not a positive duration", "  a budget of zero or less is not 'unlimited'; "+hintText(name))
		return 0
	}
	return d
}

func hintText(name string) string {
	h := hintFor(name)
	if h == "" {
		return "\n"
	}
	return strings.TrimPrefix(h, "  ")
}

// parseFlags is the half that decides whether anything after it can be trusted.
// Package flag is given no stream: its error text quotes the argument it could
// not parse, raw, and its usage dump follows -- so an argument holding a
// newline would author a whole line of stderr before any code here ran.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) bool {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		refuse(stderr, " "+fs.Name(), oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-wake %s: unexpected argument %q; run: nova-wake help\n", oneline.Escape(fs.Name()), fs.Arg(0))
		return false
	}
	return true
}

// watchKillPoint is the injected kill of test 11, at the three boundaries rule
// 11 orders the poll by: after the observed values are written, after the item
// lines reach stdout, and after the printed= marks are written. A test cannot
// SIGKILL a function it is calling, and what has to be proved is which side of
// each boundary the state lands on. It is never set outside a test, and it is a
// var in this package rather than an environment variable for the reason
// serveKillPoint is.
var watchKillPoint string

// watcher is the loop.
type watcher struct {
	stdout, stderr io.Writer
	clock          wake.Clock
	st             *wake.State
	statePath      string
	maxLines       int
	finalOnly      bool
	baseline       bool
	cold           bool
	sources        []*polled
	bus            *wake.Bus
	lines          *wake.Lines
	busDir         string
	every          time.Duration
	lineDue        time.Time
	timeout        time.Duration
	// advancer is item 3a's transaction, and its presence IS the flag: there is
	// no second field saying the same thing, because two spellings of one fact
	// are one chance for them to disagree.
	advancer *wake.Advancer

	head, headAt string
	// refusal holds the keys of lines this call read from a poll that ENDED
	// BADLY. They are printed like any other line -- rule 7 is absolute -- and
	// they are not a change: a bus that refused every read must reach the
	// rule-8 streak rather than returning the call as news at after=0s, which
	// is the tick loop this tool exists to delete.
	refusal       map[string]bool
	killed        bool
	busRead       bool
	busFail       bool
	failing       map[string]bool
	notes         map[string]bool
	changed       map[string]int
	standing      []string
	polls         int
	firstPoll     bool
	sourcePrinted bool
}

func (w *watcher) note(text string) {
	if w.notes == nil {
		w.notes = map[string]bool{}
	}
	if w.notes[text] {
		return
	}
	w.notes[text] = true
	fmt.Fprintf(w.stdout, "WAKE NOTE %s\n", oneline.Escape(text))
}

// loop is the whole watch: poll what is due, queue what moved, print from the
// head of the queue, and answer with one of the three verdicts.
func (w *watcher) loop(ctx context.Context, start time.Time, max time.Duration, onDeadline string) int {
	deadline := start.Add(max)
	w.changed = map[string]int{}
	// Step 4, before anything else is polled: a call that finds the marker
	// spools every unprinted note off the reader's OWN OPEN list, because a
	// plain inbox does not re-list a note the cursor has passed. The head is
	// read first, because rule 6 is that every change line carries the identity
	// of the thing that changed -- a note's id AND COMMIT SHA -- and a recovered
	// note is a change line like any other.
	if w.bus != nil {
		w.head, w.headAt = wake.Head(ctx, w.busDir, w.timeout)
	}
	w.recoverAdvance(ctx, start)
	for {
		now := w.clock.Now()
		// The deadline is read BEFORE anything is polled, so that --max is the
		// moment this call stops and not the moment after one more poll: a
		// watch that polled at its deadline would make one more call against
		// somebody else's server for an answer it has no time to print.
		reached := !now.Before(deadline)
		w.busRead, w.busFail = false, false
		broken := ""
		if !reached {
			for _, s := range w.sources {
				if now.Before(s.due) {
					continue
				}
				s.due = now.Add(s.src.Every())
				if w.bus != nil && s.src.Name() == w.bus.Name() {
					// The bus poll may block (--refresh), and what it may
					// block for is the time to the earliest due source, at
					// most --interval -- never --gh-timeout, and never past
					// --max.
					w.bus.Budget(w.busBudget(now, deadline))
				}
				w.poll(ctx, s.src, now)
				if n, _, _ := w.st.Streak(s.src.Name()); n >= 3 && broken == "" {
					broken = s.src.Name()
				}
			}
			if w.lines != nil && !now.Before(w.lineDue) {
				// Rule 3: --interval "is the cadence for the bus, for --line
				// and for report directories". The --line view is a git log
				// against the bus checkout, and running it on the loop's own
				// cadence meant a 5s entry interval beside an 8m --interval ran
				// it ninety-six times for every once the caller asked for.
				w.lineDue = now.Add(w.every)
				w.pollLines(ctx, now)
			}
			w.firstPoll = true
		}

		// Rule 11, step 1: every observation is in the state before anything is
		// printed. A kill here leaves the entry pending, which is a repeated
		// wake and never a lost one.
		w.save()
		if watchKillPoint == "after-observed" {
			return 0
		}
		printed, news := w.printQueue(now)
		_ = printed
		if w.killed || watchKillPoint == "after-marks" {
			return 0
		}

		// Steps 2 and 3 of the transaction, and ONLY as part of a bus poll that
		// happened before the deadline. The four steps are defined per bus poll
		// ("a poll with --advance-cursor is a transaction in four steps"), and
		// rule 1 is "The tool never waits past --max" -- an advance on the
		// deadline iteration is a write-side call, and a fetch, after the call
		// was supposed to have ended; an advance on an iteration that polled
		// only the entries is a cursor moved by something that is not a bus
		// poll at all.
		// The deadline is read AGAIN here, after the poll: a poll may spend its
		// whole budget, and rule 1 -- "The tool never waits past --max" -- is
		// about the moment the advance would start, not about the moment the
		// iteration began. An advance is a fetch and the one write-side call
		// this tool makes.
		if w.clock.Now().Before(deadline) && w.busRead {
			if w.advanceOrDefer(ctx, now) {
				// The injected kill of test 11: the process died between the
				// advance returning and the write of its output.
				return 0
			}
			if !w.busFail {
				// Both halves of the bus poll answered.
				delete(w.failing, "bus")
				w.st.ClearFail("bus")
			}
			// The advance is the bus source too, and its streak is read AFTER
			// it has had its turn: an advance that fails every call must reach
			// three and say BROKEN rather than running to the deadline quiet.
			if broken == "" {
				if n, _, _ := w.st.Streak("bus"); n >= 3 {
					broken = "bus"
				}
			}
		}
		switch {
		case broken != "":
			n, since, reason := w.st.Streak(broken)
			w.sourceLine()
			fmt.Fprintf(w.stdout, "WAKE BROKEN source=%s failures=%d since=%s: %s\n",
				oneline.Field(broken), n, oneline.Field(since),
				oneline.Escape(oneline.Cap(reason, oneline.TailBytes)))
			return 2
		case news > 0:
			w.sourceLine()
			fmt.Fprintf(w.stdout, "WAKE CHANGE after=%s polls=%d bus=%d entries=%d reports=%d lines=%d pending=%d\n",
				oneline.Field(wake.Dur(now.Sub(start))), w.polls, w.changed["bus"], w.changed["entries"],
				w.changed["reports"], w.changed["lines"], w.st.Pending())
			return 0
		case reached:
			w.sourceLine()
			// A source that could not be read at all must not end a call as
			// CALM: BROKEN arrives on the third consecutive failure, and a
			// --max under three intervals would report a watch of nothing as a
			// deadline reached.
			fmt.Fprintf(w.stdout, "WAKE QUIET after=%s polls=%d default=%s sources-failing=%d: deadline, default taken\n",
				oneline.Field(wake.Dur(now.Sub(start))), w.polls, oneline.Field(onDeadline), len(w.failing))
			return 0
		}
		w.clock.Sleep(w.until(now, deadline))
	}
}

// until is the sleep: to the earliest due source, and never past the deadline.
// A 5s bus interval beside an 8m entry interval polls the bus every 5s and the
// entry every 8m.
func (w *watcher) until(now, deadline time.Time) time.Duration {
	next := deadline
	for _, s := range w.sources {
		if s.due.Before(next) {
			next = s.due
		}
	}
	if w.lines != nil && w.lineDue.Before(next) {
		next = w.lineDue
	}
	d := next.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// busBudget is the time to the earliest source that will be due NEXT, capped by
// the deadline. A source that is due right now is being polled in this same
// iteration, so what the bus may block for is when that source comes round
// again -- `until` answers 0 for it, and a wait of nothing is not what "the
// time to the earliest due source" means. A 30s bus interval beside a 5s entry
// interval blocks 5s, and a --max inside the interval blocks to --max.
func (w *watcher) busBudget(now, deadline time.Time) time.Duration {
	next := deadline
	for _, s := range w.sources {
		due := s.due
		if !due.After(now) {
			due = now.Add(s.src.Every())
		}
		if due.Before(next) {
			next = due
		}
	}
	if w.lines != nil {
		due := w.lineDue
		if !due.After(now) {
			due = now.Add(w.every)
		}
		if due.Before(next) {
			next = due
		}
	}
	d := next.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// poll runs one source and turns what it saw into queue records.
func (w *watcher) poll(ctx context.Context, src wake.Source, now time.Time) {
	w.polls++
	if w.bus != nil && src.Name() == w.bus.Name() {
		w.head, w.headAt = wake.Head(ctx, w.busDir, w.timeout)
	}
	res, err := src.Poll(ctx, now)
	// The items are classified whether or not the poll ended badly: a run that
	// dropped an INBOX REFUSED line because nova-bus exited 1 would be the grep
	// that gave the window thirty minutes of false quiet. What a FAILED poll's
	// lines are not is NEWS.
	w.observe(src.Name(), res, now, err != nil)
	if err != nil {
		if w.failing == nil {
			w.failing = map[string]bool{}
		}
		w.failing[src.Name()] = true
		n, since, _ := w.st.Fail(src.Name(), oneLine(err.Error()), now)
		fmt.Fprintf(w.stderr, "WAKE POLL %s: %s (failure %d of 3 in a row, since %s)\n",
			oneline.Field(src.Name()), oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)), n, oneline.Field(since))
		return
	}
	delete(w.failing, src.Name())
	if w.advancer != nil && w.bus != nil && src.Name() == w.bus.Name() {
		// The bus source's poll is not over: its advance runs after the print,
		// and a read that worked beside an advance that did not is not a
		// success of this source. The streak is cleared once both halves have
		// had their turn.
		w.busRead = true
		return
	}
	w.st.ClearFail(src.Name())
}

// markRefusal remembers the keys a failed poll produced, so that printing them
// does not end the call as a change -- and FORGETS them the moment a poll that
// could be read produces the same key, because the refusal is about that poll
// and not about the key forever. A source that failed once and then answered
// has its real change counted.
func (w *watcher) markRefusal(key string, failed bool) {
	if w.refusal == nil {
		w.refusal = map[string]bool{}
	}
	if failed {
		w.refusal[key] = true
		return
	}
	delete(w.refusal, key)
}

func (w *watcher) pollLines(ctx context.Context, now time.Time) {
	res, err := w.lines.Poll(ctx, now)
	if err != nil {
		fmt.Fprintf(w.stderr, "WAKE POLL lines: %s\n", oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)))
		return
	}
	w.observe("lines", res, now, false)
}

// observe is the one comparison, in one place: the observed value against the
// stored newest one, byte for byte.
func (w *watcher) observe(source string, res wake.Result, now time.Time, failed bool) {
	w.standing = append(w.standing, res.Standing...)
	for _, key := range res.StandingKeys {
		// The same recency the sighting memory is evicted by: a standing line
		// ages only while it is not standing.
		w.st.Sight(key)
	}
	for _, it := range res.Items {
		w.markRefusal(it.Key, failed)
		value, display := it.Value, it.Value
		switch it.Kind {
		case wake.KindBus:
			value = wake.WithCommit(value, w.head)
			display = value
		case wake.KindReport:
			_, had := w.st.Newest(it.Key)
			// mtime:size and nothing else is the compared identity.
			value = wake.ReportIdentity(it.Value)
			// The LINE keeps the count: it is what lets the window tell a stub
			// from a finding without opening the file, and it is display.
			// new or modified is a fact about the STATE and not about the
			// file, so it rides on the line and never on the identity: a
			// second poll of an unchanged report must be quiet.
			display = wake.NewOrModified(it.Value, had)
		}
		// The cold-start rule: with no state file, the first poll of --entry
		// and --reports RECORDS the world and reports nothing, because a cold
		// watch would otherwise return instantly with a listing of everything
		// that exists, which is not what "wake me on a change" means.
		//
		// The bus is the exception and it is not a choice: a cold first poll
		// that swallowed five notes to stay quiet is the failure of 2026-09-11
		// caused deliberately. An OFFLINE line is the other: a line that is
		// already silent when the watch starts is exactly the news rule 2
		// exists for, and a watcher that recorded it quietly would hold the
		// silence it was asked to report.
		//
		// A line that is BACK is not. No sentence of this spec makes the first
		// sighting of a HEALTHY line a change, and a cold watch that printed
		// one WAKE LINE per online line and returned at once is the "listing of
		// everything that exists" the cold-start rule forbids.
		if w.cold && !w.firstPoll && it.Kind != wake.KindBus && it.Kind != wake.KindBusLine &&
			!(it.Kind == wake.KindLine && wake.LineIsOffline(value)) {
			w.st.RecordOnly(it.Key, value)
			continue
		}
		// --final-only keeps a non-final entry's value out of the queue AT
		// OBSERVATION TIME, because that is what the flag asked for. It
		// suppresses the wake and not the state: the value is stored on every
		// poll as always, so the churn is never re-reported as news later. It
		// never suppresses an unreadable one -- a flag that asks for fewer
		// wakes about arithmetic is not a flag that asks to sleep through a
		// source that cannot be read.
		if w.finalOnly && it.Kind == wake.KindEntry {
			if bad, _ := wake.Unreadable(value); !bad && !wake.IsFinal(value) {
				w.st.RecordOnly(it.Key, value)
				continue
			}
		}
		if w.st.ObserveDisplay(it.Key, value, display, it.DeliveryID()) && !w.refusal[it.Key] {
			// The verdict's counts are about the WORLD: a line from a poll that
			// could not be read is not something that changed in it.
			w.changed[source]++
		}
	}
	w.st.Evict()
}

func (w *watcher) save() {
	if err := w.st.Save(w.statePath); err != nil {
		fmt.Fprintf(w.stderr, "WAKE POLL state: %s\n", oneline.Err(err))
	}
}

// printQueue is rule 11, steps 2 and 3: print the item lines from the head of
// the queue up to the cap, then delete each printed record and write its
// printed= mark. The marks are written AFTER the print and never before, so a
// kill at any boundary leaves the entry pending.
func (w *watcher) printQueue(now time.Time) (int, int) {
	// One cap PER KIND, from internal/bounded, because a flat cap over a
	// concatenated stream means the loud kind eats the quiet one and the quiet
	// one is the finding the window did not already know about: forty churning
	// entries must not hide one note. The MORE lines are written here rather
	// than by bounded.Group.More, because this tool's grammar carries n=<k> --
	// the lines this poll did not print, every one of them pending.
	g := bounded.Grouped(w.stdout, w.maxLines, "WAKE", "")
	var printed []wake.Record
	for _, r := range w.st.Queue() {
		kind := wake.CapKind(w.kindOf(r.Key))
		l := g.List(kind)
		before := 0
		if l != nil {
			before = l.Shown()
		}
		g.Line(kind, wake.Render(w.kindOf(r.Key), r.Key, r.Value, now))
		if g.List(kind).Shown() > before {
			printed = append(printed, r)
		}
	}
	// The standing lines print on every poll they stand and are counted against
	// the bus cap on each of them, so a run of many polls over a refusing bus
	// still prints at most --max-lines bus lines per poll.
	for _, line := range w.standing {
		g.Line("bus", line)
	}
	w.standing = nil
	for _, kind := range g.Kinds() {
		l := g.List(kind)
		if l.Elided() > 0 {
			fmt.Fprintf(w.stdout, "WAKE MORE kind=%s shown=%d total=%d n=%d %s\n",
				oneline.Field(kind), l.Shown(), l.Total(), l.Elided(), oneline.Escape(remedyFor(kind)))
		}
	}
	if watchKillPoint == "after-lines" {
		// The kill lands between the item lines reaching stdout and the marks
		// that say so. Rule 11 chooses this side every time: the entry stays
		// pending, the next call prints it again, and a window told the same
		// news twice reads twice. The LOOP ends here too -- a process that died
		// does not poll again -- so the killed call prints each line once and
		// the test can tell a kill from a second print.
		w.killed = true
		return len(printed), 0
	}
	news := 0
	for _, r := range printed {
		w.st.MarkPrinted(r)
		if !w.refusal[r.Key] {
			news++
		}
	}
	if len(printed) > 0 {
		w.save()
	}
	return len(printed), news
}

// recoverAdvance is step 4 of the advance transaction, run once at the start of
// a call that finds bus:advance=inflight: a plain inbox for carrying=, then the
// whole carried list off the reader's own OPEN list, spooled before anything
// else -- because a plain inbox does not re-list a note the cursor has passed.
func (w *watcher) recoverAdvance(ctx context.Context, now time.Time) {
	if w.advancer == nil || !wake.Interrupted(w.st) {
		return
	}
	res, note, err := w.advancer.Recover(ctx, w.st)
	w.observe("bus", res, now, err != nil)
	w.save()
	if err != nil {
		w.busFailed(err, now)
		return
	}
	if note != "" {
		w.note(note)
	}
}

// advanceOrDefer is steps 2 and 3. Mail consumed is mail spooled; mail spooled
// is mail printed, under the cap like everything else; and a cap that elides a
// note DEFERS THE FETCH rather than losing the note. It answers whether the
// injected kill of test 11 landed.
func (w *watcher) advanceOrDefer(ctx context.Context, now time.Time) bool {
	if w.advancer == nil {
		return false
	}
	if n := wake.BusQueued(w.st); n > 0 {
		w.note(fmt.Sprintf("bus advance deferred: pending=%d bus notes unprinted; nothing fetches until they print", n))
		return false
	}
	res, killed, err := w.advancer.Advance(ctx, w.st, w.head, w.save)
	if killed {
		return true
	}
	w.observe("bus", res, now, err != nil)
	w.save()
	if err != nil {
		w.busFailed(err, now)
		return false
	}
	// Cleared ONLY after the advance's output is durable.
	wake.ClearAdvance(w.st)
	w.save()
	return false
}

// busFailed counts an advance or a recovery that could not be run toward the
// bus source's streak. "A source fails when the bus's nova-bus exits other than
// 0 or times out" -- and an advance that fails every call is a watcher that
// never fetches, which is the blindness rule 12 names.
func (w *watcher) busFailed(err error, now time.Time) {
	if w.failing == nil {
		w.failing = map[string]bool{}
	}
	w.failing["bus"] = true
	w.busFail = true
	n, since, _ := w.st.Fail("bus", oneLine(err.Error()), now)
	fmt.Fprintf(w.stderr, "WAKE POLL bus: %s (failure %d of 3 in a row, since %s)\n",
		oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)), n, oneline.Field(since))
	w.save()
}

// remedyFor names the flag that lifts the ceiling, or the file that holds the
// whole list. A cap with no remedy is censorship; a cap with one is an index.
func remedyFor(kind string) string {
	switch kind {
	case "bus":
		return "pending; the next call prints them, the cursor waits (--max-lines 0 prints them all; your own OPEN list holds the whole of what you carry)"
	case "report":
		return "pending; the next call prints them (--max-lines 0 prints them all; the --reports directories hold the files themselves)"
	default:
		return "pending; the next call prints them (--max-lines 0 prints them all)"
	}
}

// kindOf answers what kind of thing a state key names. It is read off the key's
// namespace rather than remembered, so a queue record written by an earlier
// CALL renders correctly in this one.
func (w *watcher) kindOf(key string) string {
	switch {
	case strings.HasPrefix(key, "bus:note:"):
		return wake.KindBus
	case strings.HasPrefix(key, "bus:line:"):
		return wake.KindBusLine
	case strings.HasPrefix(key, "entry:"):
		return wake.KindEntry
	case strings.HasPrefix(key, "report:"):
		return wake.KindReport
	case strings.HasPrefix(key, "line:"):
		return wake.KindLine
	}
	return ""
}

// sourceLine is rule 7's four counts, once per run, before the verdict. They
// add up: read equals the sum of the other three. A bus that printed nothing
// and a bus that printed twelve bookkeeping lines must not look the same.
func (w *watcher) sourceLine() {
	if w.bus == nil || w.sourcePrinted {
		return
	}
	w.sourcePrinted = true
	read, suppress, relay, standing := w.bus.Counts()
	fmt.Fprintf(w.stdout, "WAKE SOURCE bus read=%d suppressed=%d relayed=%d standing=%d head=%s head-at=%s\n",
		read, suppress, relay, standing, oneline.Field(dash(w.head)), oneline.Field(dash(w.headAt)))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

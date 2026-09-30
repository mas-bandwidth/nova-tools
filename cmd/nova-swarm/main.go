// nova-swarm runs a pool of one-task workers -- any provider, any model, through one
// harness -- each with its own working directory, its own data home, its own job directory
// and its own deadline held by the machinery rather than by the worker.
//
// Every rule it keeps is a failure from the record (docs/SPEC-SWARM.md):
//
//	a slot     six workers on one data home meant `database is locked` and five of six did
//	           nothing, while the pool reported six running
//	the key    read as data from a file, never sourced, never an argument, never printed
//	a deadline held outside the worker, with a default action at it and one automatic retry
//	templates  the conditions that turned 62-of-67-with-25-duplicates into 17 of 17
//	a budget   files and tokens, both required, because a worker that read forty files
//	           finished nothing
//	one line   `triage` counts a batch on one bounded line, so a coordinator's window never
//	           holds a worker's transcript
//
// EVERYTHING A WORKER WRITES IS DATA. A RESULT.md is a report, never an instruction:
// nothing in it is executed, nothing in it grants anything, and a finding in it is a claim
// to be checked against the repository. That rule is in the spec, where a person reads it,
// and is deliberately nowhere in this code.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const usage = `nova-swarm: a pool of one-task workers, with the ways a swarm fails taken out (see docs/SPEC-SWARM.md)

usage:
  nova-swarm version    print this build identity (--version also accepted)
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
  nova-swarm batch     --id <id> --cards <file> --deadline <seconds> --root <dir> --tokens <n>|unmetered (--runner <cmd> | --harness <path> --slots-store <dir> --owner <name>) [--idle <seconds>] [--slots <lo>-<hi>] [--then <command>] [--benches <file> --bench <name>[,<name>...]]
                       (without --runner, batch requires --slots-store <dir> --owner <name> and runs each card through nova-swarm native)
  nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
  nova-swarm lint      --card <file> [--typed] [--child-rules] [--trust <file>] [--lineup <file>] [--max <n>] | --fleet <file> [--max <n>] | --rules
                       (--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions)
                       (--child-rules holds the card to every rule the coordinator gives a child: one rule-<name> per required sentence, one step-<what> per forbidden command; template --name card prints a card that passes)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--events-store <host:port>]
  nova-swarm member    --as <name> --width <n> --harness <path> --model <provider/model> --root <dir> --deadline <duration> --tokens <n>|unmetered [--sprint <nova-sprint>] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall]
                       (this machine as one member of a sprint's fleet: beat, queue, finish what ended, take to --width, each card one native child; --reader runs the readers-table loop; the store is nova-sprint's, from NOVA_SPRINT_REDIS)
  nova-swarm route     --card <file> --routes <routes.tsv> [--floor 0.9] [--default <worker json>] [--key-env <name>] [--base-url <url>]
  nova-swarm slots init --store <dir> --owner <name> --capacity <n> --share <n>
  nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration> [--label <text>] [--kind <kind>]
  nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]
                       (a lease whose holder is still RUNNING is KEPT: SLOTS KEPT, live=<n>, exit 2.
                        --force frees it anyway and can oversubscribe the bench: an operator's act,
                        never a card's and never a manager's default)
  nova-swarm slots list --store <dir>
  nova-swarm worker    check <description.json> [--env] [--max <n>]

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a verification that failed, a lint that found a defect; 2 could not run:
a missing flag, an unreadable worker description, a key file that is
absent or empty, a bad invocation.

NO GUESSED ANYTHING. There is no default pool, no default worker description, no
default number of workers, no default deadline, and no default token budget.
batch takes --cards; native and route require --card; lint takes --card, or
--fleet or --rules instead; verify takes --card as an option and reads it only
when given (because a card this tool chose would be a guess about somebody
else's task); the remaining verbs take no card flag. --tokens is required on
batch and native because a budget this tool supplied would be a guess about
somebody else's task, and --tokens unmetered is a caller's statement that this
provider has no live accounting and the deadline is the only stop. Zero is
refused for tokens.

THE KEY IS READ AS DATA AND NEVER SOURCED. It lives in one file the worker
description names -- one line, the bare key or NAME=<key>, mode 0600 -- and it is
never an argument, never a log line, never in a file this tool writes. The
harness config this tool writes carries the variable's NAME, never its value.

EVERY JOB RUNS INSIDE nova-sandbox (docs/SPEC-SANDBOX.md). The job directory and
its data home are the only writable paths; the slot directory and whatever
read_roots names in the worker description are readable; the key file, ~/.ssh and
the gh configuration are in neither list and the kernel denies them.
A command that runs outside the wall and dies
inside it is missing a read_roots entry.

example:
  nova-swarm template --name read-pr
`

// helpVerbs are the verbs `help <verb>` answers with that verb's help, the same text
// `<verb> -h` prints.
var helpVerbs = map[string]bool{
	"version": true, "doctor": true, "batch": true, "verify": true, "lint": true,
	"template": true, "profile": true, "native": true, "member": true, "route": true, "slots": true, "worker": true,
}

// refuse is what an unusable invocation costs: ONE line naming what was wrong and the door
// to the usage, never the banner, which is 60 lines and is behind `nova-swarm help`.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-swarm%s: %s; run: nova-swarm help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	args := os.Args[1:]
	// Before a launch verb starts a card, refuse a nova-swarm whose PATH copy is not the
	// built one (doctor.go). The check is at the process boundary because it reads the real
	// PATH and the real home; the dispatcher below is what the tests drive with fakes.
	if code, stop := preflightDoctor(args, os.Stderr); stop {
		os.Exit(code)
	}
	os.Exit(run(args, os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0, before
	// anything is read, dialed or written (the CLI style's rule (b), #4505). Only -h:
	// every other exit of this tool is unchanged.
	defer verbflag.Recover(stdout, "nova-swarm", usage, &code)
	// --seat <name> (or NOVA_SEAT): the Redis login is read from that seat's
	// file through nova-secrets' library, in this process (#4052).
	args, err := seatcred.FromArgs(args, os.Getenv)
	if err != nil {
		return refuse(stderr, "", err.Error())
	}
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `template --name read-pr` is the one that only looks")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		// help <verb> for a NAMED verb only: anything else is the banner.
		if cmd == "help" && len(rest) > 0 && helpVerbs[rest[0]] {
			return run(append(append([]string{}, rest...), "--help"), stdin, stdout, stderr, now)
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	case "doctor":
		return cmdDoctor(rest, stdout, stderr)
	case "batch":
		return cmdBatch(rest, stdout, stderr)
	case "verify":
		return cmdVerify(rest, stdout, stderr)
	case "lint":
		return cmdLint(rest, stdout, stderr)
	case "template":
		return cmdTemplate(rest, stdout, stderr)
	case "native":
		return cmdNative(rest, stdout, stderr)
	case "member":
		return cmdMember(rest, stdout, stderr)
	case "route":
		return cmdRoute(rest, stdout, stderr)
	case "slots":
		return cmdSlots(rest, stdout, stderr)
	case "profile":
		return cmdProfile(rest, stdout, stderr)
	case "worker":
		return cmdWorker(rest, stdout, stderr)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", cmd))
}

// ------------------------------------------------------------------------------- flags

// flags is one verb's flag set with package flag's two mouths closed: its error text quotes
// the argument it could not parse, and its usage dump is discarded, so an argument beginning
// with a dash cannot author a line of stderr before any code here runs.
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

// stringListValue is a repeatable flag: every occurrence appends to the slice, so --repo a
// --repo b gives ["a" "b"]. It backs a list-valued frozen-config field (the native run's
// repos and recipients) with no default.
type stringListValue struct{ dst *[]string }

func (s stringListValue) String() string { return strings.Join(*s.dst, ",") }

func (s stringListValue) Set(v string) error {
	*s.dst = append(*s.dst, v)
	return nil
}

// secondsFlag is a duration flag a caller may write either as a whole number of seconds
// ("3", the historical spelling) or as a Go duration ("250ms"), so a caller that bounds a
// wait under a second can say so and every existing caller is unchanged.
type secondsFlag struct{ d time.Duration }

func newSecondsFlag(fs *flag.FlagSet, name string, def time.Duration) *secondsFlag {
	v := &secondsFlag{d: def}
	fs.Var(v, name, "")
	return v
}

func (s *secondsFlag) String() string { return s.d.String() }

func (s *secondsFlag) Set(v string) error {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil {
		s.d = time.Duration(n) * time.Second
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	s.d = d
	return nil
}

// parse runs the flag set. It reports nothing about required flags: those are checked by
// want and wantCount, which collect EVERY independent problem so that one run names them
// all (ONBOARDING point 2) rather than sending a first run back three times.
func (f *flags) parse(args []string, stderr io.Writer) bool {
	if err := verbflag.Parse(f.fs, args); err != nil {
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-swarm %s: takes no positional arguments, got %d (flags come before arguments)\n", f.verb, n)
		return false
	}
	return true
}

// want records a missing required flag with what it WANTS, never only what was wrong.
func (f *flags) want(value, name, wants string) {
	if strings.TrimSpace(value) == "" {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", name, wants))
	}
}

// wantCount is the same for a count whose floor is one: zero is refused rather than read as
// "unlimited" or as "none".
func (f *flags) wantCount(value int, name, wants string) {
	if value < 1 {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required and is at least 1, got %d; it wants %s; refusing to guess", name, value, wants))
	}
}

func (f *flags) add(problem string) { f.problems = append(f.problems, problem) }

// refused prints every problem this run found, one line each, and reports whether there
// were any. Three independent flags cost one run, not three.
func (f *flags) refused(stderr io.Writer) bool {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-swarm %s: %s\n", f.verb, oneline.Escape(p))
	}
	return len(f.problems) > 0
}

// tokens reads --tokens, which is a number or the explicit word `unmetered` and has no
// default: a budget nothing observes is a promise the tool cannot keep, and a caller saying
// so out loud is the only way this tool will run without one.
func (f *flags) tokens(value string) (int, bool) {
	switch {
	case strings.TrimSpace(value) == "":
		f.add("--tokens is required; it wants a token budget for this job, or the word `unmetered` when this provider has no live accounting and the deadline is the only stop; refusing to guess")
		return 0, false
	case value == "unmetered":
		return 0, true
	}
	// THE WHOLE WORD. fmt.Sscanf("%d") accepts a numeric prefix, so --tokens 50oops
	// used to launch as 50 (review finding on PR #2131). strconv.Atoi reads the full
	// string and refuses overflow, so a trailing junk, a decimal, and a number that
	// does not fit in int are all the same refusal.
	n, err := strconv.Atoi(strings.TrimSpace(value))
	switch {
	case err != nil:
		f.add(fmt.Sprintf("--tokens wants a number of tokens or the word `unmetered`, got %q", value))
	case n < 1:
		f.add(fmt.Sprintf("--tokens is a budget and is at least 1, got %d; `unmetered` is how a caller says there is no accounting", n))
	}
	return n, false
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n, err
}

// maxFlag is the ceiling every listing here carries.
func maxFlag(fs *flag.FlagSet) *int {
	return fs.Int("max", bounded.Default, "at most this many item lines, 0 for all")
}

// wantMax refuses a NEGATIVE ceiling. Zero already means all, so a negative number is a typo
// with two readings -- and the reading this tool took was "all", on the one flag whose job
// is to bound output, at the largest state (the new-user audit, F6, 2026-09-11).
func (f *flags) wantMax(value int) {
	if value < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all, so a negative ceiling is a typo with two readings and this tool refuses to pick one", value))
	}
}

// ------------------------------------------------------------------------------- verbs

type batchFlags struct {
	tokens        *string
	deadline      *string
	cards         *string
	runner        *string
	id            *string
	root          *string
	idle          *int
	maxInflight   *int
	stallAfter    *int
	benches       *string
	bench         *string
	then          *string
	harness       *string
	auth          *string
	slots         *string
	slotsStore    *string
	slotOwner     *string
	route         *bool
	noRoute       *bool
	routeReason   *string
	routeRegistry *string
	routeFloor    *float64
	routeLog      *string
	routeUsage    *string
	routeKeyEnv   *string
	routeBaseURL  *string
	workerFile    *string
}

func batchFlagSet() (*flags, *batchFlags) {
	f := newFlags("batch")
	// --max-inflight caps how many of this batch's cards run against ONE provider/model/key
	// at a time, and --stall-after ends a card that has produced no first token in that
	// many seconds, so a free tier that queues forever does not hold the batch. Both default
	// to zero, which is off.
	//
	// With routing, the model a card is dispatched with is the ladder's answer rather than
	// the string the fill script wrote in the TSV, and the TSV's model is the fallback.
	// Routing is the launcher's default: --route is kept for callers that spell it out, and
	// --no-route --reason is the one way out. --route-log and --route-usage name the
	// accounting home, and a call nobody can account for is not made: with a key set and
	// either flag missing the rules answer and the receipt says why=no-accounting; with no
	// key it says why=no-key, and with no registry (or one with no rungs) why=no-ladder.
	bf := &batchFlags{
		tokens:        f.fs.String("tokens", "", ""),
		deadline:      f.fs.String("deadline", "", ""),
		cards:         f.fs.String("cards", "", ""),
		runner:        f.fs.String("runner", "", ""),
		id:            f.fs.String("id", "", ""),
		root:          f.fs.String("root", "", ""),
		idle:          f.fs.Int("idle", 300, ""),
		maxInflight:   f.fs.Int("max-inflight", 0, ""),
		stallAfter:    f.fs.Int("stall-after", 0, ""),
		benches:       f.fs.String("benches", "", ""),
		bench:         f.fs.String("bench", "", ""),
		then:          f.fs.String("then", "", ""),
		harness:       f.fs.String("harness", "", ""),
		auth:          f.fs.String("auth", "", ""),
		slots:         f.fs.String("slots", "", ""),
		slotsStore:    f.fs.String("slots-store", "", ""),
		slotOwner:     f.fs.String("owner", "", ""),
		route:         f.fs.Bool("route", false, ""),
		noRoute:       f.fs.Bool("no-route", false, ""),
		routeReason:   f.fs.String("reason", "", ""),
		routeRegistry: f.fs.String("route-registry", "", ""),
		routeFloor:    f.fs.Float64("route-floor", swarm.DefaultRouteFloor, ""),
		routeLog:      f.fs.String("route-log", "", ""),
		routeUsage:    f.fs.String("route-usage", "", ""),
		routeKeyEnv:   f.fs.String("route-key-env", decide.DefaultKeyEnv, ""),
		routeBaseURL:  f.fs.String("route-base-url", decide.DefaultBaseURL, ""),
		workerFile:    f.fs.String("worker", "", ""),
	}
	return f, bf
}

func cmdBatch(args []string, stdout, stderr io.Writer) int {
	f, bf := batchFlagSet()
	tokens := bf.tokens
	deadline := bf.deadline
	cards := bf.cards
	runner := bf.runner
	id := bf.id
	root := bf.root
	idle := bf.idle
	maxInflight := bf.maxInflight
	stallAfter := bf.stallAfter
	benches := bf.benches
	bench := bf.bench
	then := bf.then
	harness := bf.harness
	auth := bf.auth
	slots := bf.slots
	slotsStore := bf.slotsStore
	slotOwner := bf.slotOwner
	route := bf.route
	noRoute := bf.noRoute
	routeReason := bf.routeReason
	routeRegistry := bf.routeRegistry
	routeFloor := bf.routeFloor
	routeLog := bf.routeLog
	routeUsage := bf.routeUsage
	routeKeyEnv := bf.routeKeyEnv
	routeBaseURL := bf.routeBaseURL
	workerFile := bf.workerFile
	if !f.parse(args, stderr) {
		return 2
	}
	// H4 (#1625): routing is the launcher's default; the only way out is an
	// explicit --no-route carrying the --reason that opens it, so a launch
	// cannot forget the route the way a sentence in a brief can.
	routeOn := *route || !*noRoute
	if *noRoute && strings.TrimSpace(*routeReason) == "" {
		f.add("--no-route needs --reason <text>: a skipped route is a fact in the log, so the reason that opens the skip is not optional")
	}
	return cmdBatchGather(f, *id, *cards, *deadline, *runner, *root, *idle, *maxInflight, *stallAfter, *benches, *bench, *then, *harness, *auth, *slots, *slotsStore, *slotOwner, *workerFile, *tokens,
		routeFlags{on: routeOn, reason: *routeReason, registry: *routeRegistry, floor: *routeFloor, log: *routeLog,
			usage: *routeUsage, keyEnv: *routeKeyEnv, baseURL: *routeBaseURL}, stdout, stderr)
}

// cmdBatchGather executes a TSV of cards: it starts one runner process per card, waits
// until they all end or the batch's deadline, and folds every card's RESULT.md into one
// bounded packet.
// routeFlags is the --route family, held together so the batch verb's own
// signature stays readable.
type routeFlags struct {
	on bool
	// reason is the --reason that opens an explicit --no-route. It is empty
	// whenever the launcher routes.
	reason   string
	registry string
	floor    float64
	log      string
	usage    string
	keyEnv   string
	baseURL  string
}

// routeInput builds the batch's route seam, or refuses with the one line that
// says what is missing. nil with no refusal means --route was not asked for.
//
// The key is read from the environment the caller names and NEVER from argv or
// a file, and it is never printed: an absent key is not a refusal, it is the
// fallback -- the rules answer, no call is made, and every card keeps today's
// model, so the loop runs on a bench with no API at all.
func routeInput(f *flags, r routeFlags, stderr io.Writer) *swarm.RouteInput {
	if !r.on {
		if strings.TrimSpace(r.reason) == "" {
			return nil
		}
		// An explicit --no-route --reason still needs the log home for its
		// skipped row, so the reason is a fact in the log and not an absence.
		return &swarm.RouteInput{Floor: r.floor, Log: r.log, Usage: r.usage}
	}
	if err := decide.ValidFloor(r.floor); err != nil {
		f.add(fmt.Sprintf("--route-floor %s is not a confidence; it wants a number between 0 and 1, such as --route-floor 0.9",
			oneline.Field(strconv.FormatFloat(r.floor, 'g', -1, 64))))
		return nil
	}
	// H4 (#1625): a missing accounting home is not a refusal. The rules answer,
	// no call is made, and the receipt says why=no-accounting; the one way out
	// of the route is --no-route --reason.
	in := &swarm.RouteInput{Floor: r.floor, Log: r.log, Usage: r.usage}
	reg, err := decide.LoadRegistry(r.registry)
	if err != nil {
		f.add(fmt.Sprintf("--route-registry: %s", oneline.Err(err)))
		return nil
	}
	in.Registry = reg
	client, err := decide.New(r.baseURL, r.keyEnv)
	if err != nil {
		// No key is no call. It is said once, by the name of the variable and
		// never by its value, and the batch runs on today's models.
		fmt.Fprintf(stderr, "BATCH NOTE route: %s; the ladder answers by its rules alone and every card keeps today's model\n", oneline.Err(err))
		return in
	}
	in.Decide = client.Decide
	return in
}

func cmdBatchGather(f *flags, id, cards, deadline, runner, root string, idle, maxInflight, stallAfter int, benches, bench, then, harness, auth, slots, slotsStore, slotOwner, workerFile, tokens string, route routeFlags, stdout, stderr io.Writer) int {
	f.want(id, "id", "the batch id; it is the packet's first token so a reader can match it to admission")
	// THE BUDGET WORD is validated by the same f.tokens as native. A non-number
	// or zero is refused before launch. What is CARRIED is the
	// word as typed: rule 13d puts it "verbatim into every `native` argv", and `native` is
	// the verb that decides what it means.
	//
	// AN ABSENT WORD IS THE BATCH'S OWN REFUSAL (#3202): f.tokens's sentence is per-job
	// ("a token budget for this job"), and the one a batch caller needs says the word is
	// for EACH card and never divided, which is swarm.NoBatchTokensRefusal. It is said
	// here, beside every other flag problem, so it reaches a caller of the binary and not
	// only a caller of swarm.Batch.
	noTokens := strings.TrimSpace(tokens) == ""
	if !noTokens {
		f.tokens(tokens)
	}
	f.want(cards, "cards", "a TSV naming one card per line: label<TAB>slot<TAB>model<TAB>card-path")
	f.want(deadline, "deadline", "a whole number of seconds, the whole batch's one deadline")
	// #636: --runner is one of two ways to run a local card; --harness (or a `local` row in
	// --benches) runs it through this binary's own `native`, so neither alone is required.
	if bench == "" && runner == "" && harness == "" && benches == "" {
		f.add("--runner or --harness is required: --runner <cmd> starts once per card with label slot model card-path root tokens, and --harness <path> runs each card through this binary's own `nova-swarm native`; refusing to guess")
	}
	if bench != "" {
		f.want(benches, "benches", "a table of one bench per row: name host root cores harness auth wall")
	}
	f.want(root, "root", "the directory a card's RESULT.md hangs under (<root>/<slot>/jobs/<label>/RESULT.md)")
	seconds := 0
	if deadline != "" {
		n, err := parseInt(deadline)
		if err != nil || n < 1 {
			f.add(fmt.Sprintf("--deadline wants a whole number of seconds, got %q; a batch whose deadline is not a wait is a typo", deadline))
		} else {
			seconds = n
		}
	}
	if idle < 1 {
		f.add(fmt.Sprintf("--idle wants a whole number of seconds, got %d; a card whose log has not grown this long is killed", idle))
	}
	routed := routeInput(f, route, stderr)
	if noTokens {
		fmt.Fprintln(stderr, swarm.NoBatchTokensRefusal)
	}
	if f.refused(stderr) || noTokens {
		return 2
	}
	var w swarm.Worker
	if strings.TrimSpace(workerFile) != "" {
		loaded, problems := swarm.LoadWorker(workerFile)
		if len(problems) > 0 {
			for _, problem := range problems {
				fmt.Fprintf(stderr, "nova-swarm batch: %s\n", oneline.Err(problem))
			}
			return 2
		}
		w = loaded
	}
	return swarm.Batch(swarm.BatchInput{
		ID: id, Deadline: time.Duration(seconds) * time.Second,
		Idle:        time.Duration(idle) * time.Second,
		MaxInflight: maxInflight,
		StallAfter:  time.Duration(stallAfter) * time.Second,
		Cards:       cards, Root: root, Runner: runner,
		Benches: benches, Bench: bench, Then: then,
		Harness: harness, Auth: auth, Slots: slots,
		SlotsStore: slotsStore, SlotOwner: slotOwner,
		Tokens:    tokens,
		Route:     routed,
		RouteSkip: route.reason,
		Worker:    w,
		Stdout:    stdout, Stderr: stderr,
	})
}

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	f := newFlags("verify")
	result := f.fs.String("result", "", "")
	contract := f.fs.String("contract", "", "")
	label := f.fs.String("label", "", "")
	card := f.fs.String("card", "", "")
	runRecord := f.fs.String("run-record", "", "")
	usageFile := f.fs.String("usage", "", "")
	max := f.fs.Int("max", swarm.DefaultContractLines, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*result, "result", "the path to the job's RESULT.md whose line 1 is to be checked")
	f.want(*contract, "contract", "the card's contract line, which line 1 of RESULT.md must equal exactly")
	f.want(*label, "label", "the job's label, carried on the result line and in the receipt")
	if *max < 1 {
		f.add(fmt.Sprintf("--max is at least 1, got %d; it bounds the evidence lines past the disposition", *max))
	}
	if f.refused(stderr) {
		return 2
	}

	c := swarm.Contract{Label: *label, ContractLine: *contract, MaxLines: *max, WallSeconds: -1, ExitCode: -1}
	if *card != "" {
		raw, err := os.ReadFile(*card)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm verify: --card wants a readable file of the card text: %s\n", oneline.Err(err))
			return 2
		}
		c.Card = raw
	}
	if *runRecord != "" {
		var rec swarm.ExitRecord
		if err := swarm.ReadJSON(*runRecord, &rec); err != nil {
			fmt.Fprintf(stderr, "nova-swarm verify: --run-record wants a readable exit.json: %s\n", oneline.Err(err))
			return 2
		}
		c.HaveRun, c.ExitCode = true, rec.RC
	}
	if *usageFile != "" {
		raw, err := os.ReadFile(*usageFile)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm verify: --usage wants a readable usage file: %s\n", oneline.Err(err))
			return 2
		}
		row := swarm.UsageRow{}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) >= 2 {
			head := strings.Split(lines[0], "\t")
			values := strings.Split(lines[1], "\t")
			for i, name := range head {
				if i < len(values) {
					row[name] = values[i]
				}
			}
		}
		c.HaveUsage = true
		c.TokensIn, _ = row.Int("tokens_in")
		c.TokensOut, _ = row.Int("tokens_out")
		c.USD = dash(row["usd"])
		if started, err := time.Parse(time.RFC3339, dash(row["started"])); err == nil {
			if ended, err := time.Parse(time.RFC3339, dash(row["ended"])); err == nil {
				c.WallSeconds = int(ended.Sub(started).Seconds())
			}
		}
	}

	out, err := swarm.CheckResult(*result, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm verify: --result wants a readable RESULT.md: %s\n", oneline.Err(err))
		return 2
	}
	if err := swarm.WriteReceipt(*result+".receipt", out, c); err != nil {
		fmt.Fprintf(stderr, "nova-swarm verify: the receipt could not be written: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintln(stdout, oneline.Escape(out.Line))
	if !out.OK {
		return 1
	}
	return 0
}

func cmdTemplate(args []string, stdout, stderr io.Writer) int {
	f := newFlags("template")
	name := f.fs.String("name", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	// THE BANNER AND THE REFUSAL NAME THE SAME SET. The list was typed twice and the
	// tool answered to a fifth name (`worker`) that neither copy mentioned.
	f.want(*name, "name", "one of "+strings.Join(swarm.TemplateNames(), ", "))
	if f.refused(stderr) {
		return 2
	}
	body, err := swarm.Template(*name)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm template: %s\n", oneline.Err(err))
		return 2
	}
	// A template is printed VERBATIM because it is a document a person redirects into a
	// file, not an event line: escaping it would fold it into one unusable line. Every
	// byte of it is a constant in this binary.
	fmt.Fprint(stdout, body)
	return 0
}

// cmdProfile folds the per-turn timelines a glob names into a card's minutes per phase. It
// reads only the files the native run already wrote; it launches nothing and calls no model.
func cmdProfile(args []string, stdout, stderr io.Writer) int {
	f := newFlags("profile")
	jobs := f.fs.String("jobs", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*jobs, "jobs", "a glob of job directories (or timeline.tsv files), each holding a native run's per-turn timeline")
	if f.refused(stderr) {
		return 2
	}
	return swarm.ProfileJobs(*jobs, stdout, stderr)
}

type nativeFlags struct {
	harness         *string
	model           *string
	cardPath        *string
	slot            *string
	root            *string
	deadline        *string
	idle            *string
	label           *string
	auth            *string
	config          *string
	workerFile      *string
	sandbox         *string
	noWall          *bool
	noSharedCaches  *bool
	resultsRootFlag *string
	sweepNow        *bool
	tokensWord      *string
	usageInterval   *secondsFlag
	eventsStore     *string
	benchFlag       *string
	stageTimeout    *string
	repos           []string
	recipients      []string
}

func nativeFlagSet() (*flags, *nativeFlags) {
	f := newFlags("native")
	// --idle bounds stillness, which is not the bound on length: a card is idle only when
	// neither its own output nor its process tree has moved for this long, so a `go test`
	// printing nothing for minutes is not a dead card. The default is `batch --idle`'s 300s,
	// and 0 turns the watch off.
	//
	// --results-root is where RESULT.md, usage.tsv and the report are published; empty
	// derives <root>/results from the root this run is given. --sweep-now deletes the job
	// directory after that publish, so a bench sweep never deletes the results with the
	// working directory.
	nf := &nativeFlags{
		harness:         f.fs.String("harness", "", ""),
		model:           f.fs.String("model", "", ""),
		cardPath:        f.fs.String("card", "", ""),
		slot:            f.fs.String("slot", "", ""),
		root:            f.fs.String("root", "", ""),
		deadline:        f.fs.String("deadline", "", ""),
		idle:            f.fs.String("idle", swarm.DefaultNativeIdle.String(), ""),
		label:           f.fs.String("label", "", ""),
		auth:            f.fs.String("auth", "", ""),
		config:          f.fs.String("config", "", ""),
		workerFile:      f.fs.String("worker", "", ""),
		sandbox:         f.fs.String("sandbox", "", ""),
		noWall:          f.fs.Bool("no-wall", false, ""),
		noSharedCaches:  f.fs.Bool("no-shared-caches", false, ""),
		resultsRootFlag: f.fs.String("results-root", "", ""),
		sweepNow:        f.fs.Bool("sweep-now", false, ""),
	}
	// native takes no bench slot lease: a bench's capacity is one place, the dealer's, and a
	// second ledger here would give a second answer. --slots-store and --owner are accepted
	// so a caller that passes them is not refused on an unknown flag, and they are read by
	// nothing.
	_ = f.fs.String("slots-store", "", "")
	_ = f.fs.String("owner", "", "")
	nf.tokensWord = f.fs.String("tokens", "", "")
	nf.usageInterval = newSecondsFlag(f.fs, "usage-interval", swarm.DefaultUsageInterval)
	// --events-store names the fleet Redis this card's one `ok`/`fail` entry is XADDed to.
	// It is optional and defaults to the environment (NOVA_REDIS_ADDR, else
	// NOVA_REDIS_HOST:NOVA_REDIS_PORT), and the whole emit is SILENTLY SKIPPED unless
	// NOVA_REDIS_BENCH_PASSWORD is also in the environment. A bench that has not been given
	// the store's password must still run cards, so there is no refusal and no default host
	// (internal/events/writer.go). The password is never a flag and is never printed.
	nf.eventsStore = f.fs.String("events-store", "", "")
	nf.benchFlag = f.fs.String("bench", "", "")
	nf.stageTimeout = f.fs.String("stage-timeout", "", "")
	f.fs.Var(stringListValue{&nf.repos}, "repo", "")
	f.fs.Var(stringListValue{&nf.recipients}, "recipient", "")
	return f, nf
}

func cmdNative(args []string, stdout, stderr io.Writer) int {
	f, nf := nativeFlagSet()
	if !f.parse(args, stderr) {
		return 2
	}
	harness := nf.harness
	model := nf.model
	cardPath := nf.cardPath
	slot := nf.slot
	root := nf.root
	deadline := nf.deadline
	idle := nf.idle
	label := nf.label
	auth := nf.auth
	config := nf.config
	workerFile := nf.workerFile
	sandbox := nf.sandbox
	noWall := nf.noWall
	noSharedCaches := nf.noSharedCaches
	resultsRootFlag := nf.resultsRootFlag
	sweepNow := nf.sweepNow
	tokensWord := nf.tokensWord
	usageInterval := nf.usageInterval
	eventsStore := nf.eventsStore
	benchFlag := nf.benchFlag
	stageTimeout := nf.stageTimeout
	repos := nf.repos
	recipients := nf.recipients
	if *noWall && *sandbox != "" {
		f.add("--no-wall and --sandbox together: one asks for no containment at all and the other names the wall to use; pass at most one")
	}
	// ISSUE #881: `--worker <file>` names a worker description, and the description is the
	// source of the model and of the key. It is loaded HERE, before the flag checks, so a
	// description that is not readable is one refusal and a description whose fields are
	// wrong is the same. Without --worker, native keeps --model and --auth as today.
	var w swarm.Worker
	workerGiven := *workerFile != ""
	if workerGiven {
		loaded, problems := swarm.LoadWorker(*workerFile)
		if len(problems) > 0 {
			for _, problem := range problems {
				fmt.Fprintf(stderr, "nova-swarm native: %s\n", oneline.Err(problem))
			}
			return 2
		}
		w = loaded
		if w.Secret != "" {
			// The key is in the environment, never in a file: --auth and --config are the
			// legacy shape's, and a description that names a secret writes no auth file and
			// carries its own provider declaration. Both are refused where the caller can
			// still fix them.
			if *auth != "" {
				f.add("--auth is the legacy shape's and this worker description names a secret; the key comes from the environment and no auth file is written")
			}
			if *config != "" {
				f.add("--config is the legacy shape's and this worker description names a secret; the description's own provider declaration is written instead")
			}
		}
	}
	f.want(*harness, "harness", "the harness binary path, checked for existence and execution")
	if !workerGiven {
		f.want(*model, "model", "the model to run: provider/model, one slash, both sides nonempty; --worker <file> names a description that pins the model instead")
	}
	f.want(*cardPath, "card", "the path to the card file")
	f.want(*slot, "slot", "the slot directory this run executes in")
	f.want(*root, "root", "the configured root the slot directory must sit under")
	f.want(*deadline, "deadline", "the wall duration that kills the child (e.g. 60s, 5m)")
	// THE WORD, READ WITH EVERY OTHER FLAG AND REFUSED WITH THEM (rule 13d). It sits in
	// the collector so a caller who left out the budget AND the deadline is told both in
	// one run; and it sits HERE, above every line below that touches the disk --
	// nativeRun's own job directory, data home and temp directory -- because 13d
	// refuses "before any directory is made".
	budgetTokens, budgetUnmetered := f.tokens(*tokensWord)
	if f.refused(stderr) {
		return 2
	}
	// CARD-8349: a card budget below the harness's MEASURED startup cost is
	// refused by name before any directory is made and before any child starts.
	if workerGiven && w.HasCardBudget() {
		if sc, err := swarm.ReadStartupCost(*root); err == nil {
			if line := w.BudgetRefusal(sc); line != "" {
				fmt.Fprintln(stderr, oneline.Escape(line))
				return 2
			}
		}
	}
	d, err := time.ParseDuration(*deadline)
	if err != nil || d <= 0 {
		fmt.Fprintf(stderr, "nova-swarm native: --deadline wants a positive duration: %s\n", oneline.Err(err))
		return 2
	}
	// A BUDGET NEEDS A SOURCE THE TOOL CAN READ (rule 13d), AND THE INTERVAL HAS A FLOOR
	// AND A CEILING. Both are checked HERE: after the deadline is parsed, because the
	// interval's ceiling is the deadline; and above everything below, because 13d refuses
	// "before any directory is made" and nativeRun's first act is to make the job
	// directory. Neither check reads a file or starts a process.
	//
	// The source is the worker description's `usage`, and `opencode` when there is no
	// `--worker` -- rule 13d's own sentence, which swarm.NativeUsageSource holds so that
	// nobody retypes the default.
	var workerForBudget *swarm.Worker
	if workerGiven {
		workerForBudget = &w
	}
	if reason := swarm.NativeBudgetSourceRefusal(swarm.NativeUsageSource(workerForBudget), budgetTokens, budgetUnmetered, workerForBudget); reason != "" {
		refuseNative(stderr, reason)
		return 2
	}
	if reason := swarm.NativeUsageIntervalRefusal(usageInterval.d, d); reason != "" {
		fmt.Fprintf(stderr, "nova-swarm native: %s\n", oneline.Escape(reason))
		return 2
	}
	idleDur := swarm.DefaultNativeIdle
	if *idle != "" {
		v, ierr := time.ParseDuration(*idle)
		if ierr != nil || v < 0 {
			fmt.Fprintf(stderr, "nova-swarm native: --idle wants a duration such as 5m, or 0 for no watch: %s\n", oneline.Field(*idle))
			return 2
		}
		idleDur = v
	}
	if idleDur > d {
		idleDur = d
	}
	cardRaw, err := os.ReadFile(*cardPath)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm native: --card wants a readable file: %s\n", oneline.Err(err))
		return 2
	}
	lbl := *label
	if lbl == "" {
		lbl = strings.TrimSuffix(filepath.Base(*cardPath), filepath.Ext(*cardPath))
	}
	effectiveModel := *model
	if workerGiven && effectiveModel == "" {
		if strings.Contains(w.Model, "/") {
			effectiveModel = w.Model
		} else {
			effectiveModel = w.Provider + "/" + w.Model
		}
	}
	var stageDur time.Duration
	if *stageTimeout != "" {
		v, serr := time.ParseDuration(*stageTimeout)
		if serr != nil || v <= 0 {
			fmt.Fprintf(stderr, "nova-swarm native: --stage-timeout wants a positive duration: %s\n", oneline.Field(*stageTimeout))
			return 2
		}
		stageDur = v
	}
	cfg := nativeRunConfig{
		binary:         *harness,
		model:          effectiveModel,
		label:          lbl,
		card:           cardRaw,
		slotDir:        *slot,
		root:           *root,
		authFile:       *auth,
		configFile:     *config,
		deadline:       d,
		idle:           idleDur,
		repos:          repos,
		recipients:     recipients,
		sandbox:        *sandbox,
		noWall:         *noWall,
		noSharedCaches: *noSharedCaches,
		resultsRoot:    resultsRootOf(*resultsRootFlag, *root),
		tokens:         budgetTokens,
		unmetered:      budgetUnmetered,
		usageInterval:  usageInterval.d,
		benchName:      *benchFlag,
		stageTimeout:   stageDur,
	}
	if workerGiven {
		cfg.worker = &w
	}
	res, code := nativeRun(cfg, stderr)
	if code != 0 && !res.lost && !res.unrecorded {
		return code
	}
	// AN UNREAD DENIAL IS NEVER AN OK (issue #1465; review finding on #1478). This is the ONE
	// refusal that lands AFTER the spend, and it is a refusal rather than a token on the OK
	// line on purpose: the run of #1465 carried `rc=0 sandbox=landlock harness=ok` over a
	// card whose shell had been denied the toolchain, and a coordinator reading dispositions
	// and not prose shipped a commit nobody had compiled. There is no OK line here at all.
	//
	// THE REFUSAL ASSERTS NO CAUSE. The shell's words name a path, not an operation, so the
	// reason labels it `operation=unverified`, quotes the line, and asks for the one
	// measurement that would settle it. The job directory is named, so the work and the usage
	// row the child did produce are still harvestable.
	if (res.shellDenial != swarm.ShellDenial{}) {
		refuseNative(stderr, swarm.ShellDenialReason(cfg.label, res.job, res.wall, res.rc, res.shellDenial))
		return 2
	}
	// OK IS A VERDICT, NOT A PUNCTUATION MARK (nova-tools #1844). This line said
	// `NATIVE OK` for every run that reached it, including a run that produced NOTHING:
	// a card came back rc=1 on both attempts, zero tokens, zero
	// dollars, no RESULT.md and no repo -- and the launcher's one log line read
	// `bench card attempt=1 wall=159s NATIVE OK label=card job=...`. A fill
	// loop or a manager counting in-flight cards by that line counts a card that never
	// ran as delivered. So the word is earned: the harness has to have answered and the
	// run has to have left the one artefact a card exists to produce. When it has not,
	// the line is `NATIVE INCOMPLETE` and carries `why=` naming which of the three it
	// failed -- every other field is byte-for-byte the same, so a reader that parses
	// fields still reads them all.
	// The request may have been accepted and the response was lost. That is
	// not a delivered card and not an ordinary failure the coordinator may retry.
	verdict, why := nativeVerdictWhy(res)
	// harness=<ok|silent> is ALWAYS present (issue #591): the usage suffix is the only
	// optional tail, so a reader parses one fixed line and a silent harness is never OK.
	//
	// AND SO IS budget= (rule 13d: "`NATIVE OK` always carries `budget=`"). It sits
	// immediately after harness= and ahead of every optional tail, where the output
	// grammar puts it (docs/SPEC-SWARM.md, "Output grammar", the NATIVE OK line), so the
	// fixed part of the line stays one fixed part. It is the JOB's figure -- the sum over
	// every launch at the final read -- and never a launch's row. The VERDICT above and
	// this field are independent: budget= and stopped= are carried by an INCOMPLETE line
	// too, because #1844's own sentence is that "every other field is byte-for-byte the
	// same, so a reader that parses fields still reads them all".
	fmt.Fprintf(stdout, "NATIVE %s label=%s job=%s tmp=%s rc=%d wall=%.2fs sandbox=%s card_sha256=%s binary_sha256=%s config=%s harness=%s budget=%s%s%s%s%s",
		oneline.Field(verdict), oneline.Field(cfg.label), oneline.Field(res.job), oneline.Field(res.tmp), res.rc, res.wallSeconds, oneline.Field(res.wall), oneline.Field(res.cardSHA256), oneline.Field(res.binarySHA256), oneline.Field(dash(res.configSHA)), oneline.Field(orElse(res.harness, "silent")),
		oneline.Field(swarm.BudgetWord(cfg.unmetered, cfg.tokens, res.spent, res.observed, res.partial)),
		fenceSuffix(res.fence), usageSuffix(res.usageReason, res.usageState), termSuffix(res.terminated), stoppedSuffix(res.stopped))
	if why != "" {
		fmt.Fprintf(stdout, " why=%s", oneline.Field(why))
	}
	fmt.Fprintln(stdout)
	// THE PROMPT-DEFECT LINE, ON NATIVE'S OWN STDOUT AFTER THE `NATIVE` LINE, AND IN NO
	// FILE (rule 13d, decision 16). The pool appends it to the job's RESULT.md, creating the
	// file where the worker published none; on this route that would score the card
	// `line1-mismatch`, and 13d promises the published report is kept byte for byte -- so
	// here it is printed and nothing is written.
	if res.defect != "" {
		fmt.Fprintln(stdout, oneline.Escape(res.defect))
	}
	// THE WALL REPORT (issue #918): a run the fence stopped with no result ends `wall`,
	// and the line names the path and the commits so the harvester pushes the work.
	if res.wallReport != "" {
		fmt.Fprintln(stdout, oneline.Escape(res.wallReport))
	}
	// THE REPORT LINE A WALL DEATH OWES (issue #644's follow-up): the path the wall refused,
	// the step the card reached, and the commits it left on its branch so a harvester can
	// still push the work. Printed only when the wall stopped a card with no result, which is
	// the one shape nativeRun sets res.wall for.
	if (res.wallRefusal != swarm.WallRefusal{}) {
		branch, commits, _ := swarm.WallCommits(filepath.Join(res.job, "repo"))
		fmt.Fprintln(stdout, swarm.WallLine(cfg.label, res.wallRefusal, branch, commits))
	}
	// THE END THE WATCH GAVE THE CARD, in the words of what it actually saw. A card the
	// wall stopped says so; a card that simply went still says THAT, on a line that is
	// deliberately not a WALL line -- `js-under-20-bytes` died in a provider stall and was
	// reported as a wall death at a path it had already worked past sixteen steps earlier.
	if res.idled {
		if res.idleEnd.Refused {
			fmt.Fprintln(stdout, swarm.WallRefusedLine(cfg.label, res.idleEnd.Kind, res.idleEnd.Path, res.idleEnd.Step))
		} else {
			fmt.Fprintln(stdout, swarm.CardIdleLine(cfg.label, res.idleEnd))
		}
		if res.blockedPath != "" {
			fmt.Fprintf(stdout, "NATIVE NOTE: the card published no report of its own; one naming the block was written to %s\n", oneline.Field(res.blockedPath))
		}
	}
	// THE CARD-END ENTRY, after the receipt and after every report line, and BEFORE
	// --sweep-now can remove the job directory: cardEndEvent's classification reads the
	// card's own report from res.job (resultIsFailed), so the entry must be built while
	// that directory still exists. The emit returns no error by construction
	// (internal/events/writer.go) -- a store that is down costs one line on stderr and
	// the exit code below is the run's own, untouched.
	emitCardEnd(context.Background(), seatEventLogin(events.WriterOptions{
		Addr: *eventsStore, Log: stderr, Timeout: nativeEventTimeout,
	}), cfg, res, verdict, benchName())
	// THE CARD WRAPPER'S HAND-OFF (swarm cardout.go). Under nova-card the harness is
	// handed NOVA_CARD_OUT, and the wrapper commits $NOVA_CARD_OUT/repo and reads
	// $NOVA_CARD_OUT/RESULT.md; the card cloned into this job instead, in a slot the bench
	// chose. The repo moves there after every report line and the card-end entry have read
	// the job, and before --sweep-now can delete it. No NOVA_CARD_OUT is no hand-off.
	if res.job != "" {
		if h, err := swarm.HandOffCardOut(res.job, os.Getenv(swarm.CardOutEnv)); err != nil {
			fmt.Fprintf(stderr, "NATIVE NOTE: the card's work was not handed to %s: %s\n", oneline.Field(swarm.CardOutEnv), oneline.Escape(err.Error()))
		} else if h.Repo != "" {
			fmt.Fprintf(stderr, "NATIVE NOTE: the card's repo was handed to %s (moved=%t)\n", oneline.Field(h.Repo), h.Moved)
		}
	}
	// THE JOB IS DISPOSABLE ONLY AFTER THE RESULTS EXIST (issue #2632). --sweep-now
	// is the control: it deletes the job directory the way the bench sweep does,
	// and only when publishNativeResults named the directory it landed in. A
	// publish that did not land leaves the job, which is then the only copy.
	if *sweepNow {
		if res.resultsDir == "" {
			fmt.Fprintf(stderr, "NATIVE NOTE: --sweep-now left %s in place: its results were not published\n", oneline.Field(res.job))
		} else if err := sweepNativeJob(res.root, res.job); err != nil {
			fmt.Fprintf(stderr, "NATIVE NOTE: the job directory %s could not be removed: %s\n", oneline.Field(res.job), oneline.Escape(err.Error()))
		}
	}
	if code != 0 {
		return code
	}
	return nativeProcessExit(res.rc)
}

// nativeEventTimeout bounds the card-end emit. It is short on purpose: the card is already
// finished and its dealt seat is still counted against the bench, so a store that is not
// answering must cost seconds, never minutes.
const nativeEventTimeout = 5 * time.Second

// ------------------------------------------------------------------------------- helpers

// nativeVerdictWhy is the word on the NATIVE line and the why= that follows it.
// A lost provider body is unknown-acceptance, not a delivered card and not an
// ordinary failure the coordinator may retry. The other three whys are the
// ones a run earns when the harness did not answer with a result.
func nativeVerdictWhy(res nativeRunResult) (verdict, why string) {
	if res.lost {
		return "INCOMPLETE", "unknown-acceptance"
	}
	switch harnessState := orElse(res.harness, "silent"); {
	case harnessState == "silent":
		return "INCOMPLETE", "harness-silent"
	case !nativeLeftAResult(res.job):
		return "INCOMPLETE", "no-result"
	case res.rc != 0:
		return "INCOMPLETE", "rc"
	}
	return "OK", ""
}

// resultsRootOf is the directory native publishes into. A named --results-root
// wins. Otherwise it is <root>/results, derived from the root the run was given,
// not a path invented beside it.
func resultsRootOf(flag, root string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(root, "results")
}

// nativeProcessExit is the process exit after a launch that started. The child's
// code is already on the verdict line as rc=<n>. Passing 255 through made a fill
// loop treat a finished card as a transport failure and retry it (nova-tools
// #2058). Local ssh(1) exits 255 for any error; that is not proof the remote
// command never started, so the outcome is potentially UNKNOWN and a retry
// waits on reconciliation. A negative rc is a kill (deadline or TERM) and is
// already exit 1.
func nativeProcessExit(childRC int) int {
	if childRC == 0 {
		return 0
	}
	if childRC < 0 || childRC == 255 {
		return 1
	}
	return childRC
}

// nativeLeftAResult reports whether the run left the one artefact a card exists to produce:
// RESULT.md in its job directory, or in the clone the card worked in. A card that abstains
// still writes one (it says ABSTAIN on line 2); a run that produced nothing writes none.
//
// A REPORT THE MACHINERY WROTE IS NOT THE CARD'S (issue #2548). A card that ended its last
// turn with a question publishes nothing, and the run now writes `RESULT: ASKED <question>`
// for it so the question is not lost. That file is evidence of an ABSENCE, and counting it
// here would turn the verdict this run already prints -- `INCOMPLETE why=no-result` -- into
// `NATIVE OK` for a card that did nothing but ask, which is the very fault #1844 made this
// word earn itself. The verdict is therefore unchanged by the report, and the report is
// where the question goes.
func nativeLeftAResult(job string) bool {
	if strings.TrimSpace(job) == "" {
		return false
	}
	for _, p := range []string{
		filepath.Join(job, "RESULT.md"),
		filepath.Join(job, "repo", "RESULT.md"),
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !swarm.AskedReport(p) {
			return true
		}
	}
	return false
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// fenceSuffix renders what the harness's OWN fence did to this card (issue #644): the empty
// string when it rejected nothing, and ` fence=rejected path=<p>` naming the first path it
// auto-rejected otherwise. It is the FIELD the batch reads to score the card `fence` instead
// of `no-result` -- the machinery fenced the card off a path its own card named, which is
// nothing like a model that chose to publish nothing, and a coordinator reading `no-result`
// went looking at the model for eight cards that never got to run (2026-09-16).
func fenceSuffix(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return " fence=rejected path=" + oneline.Field(path)
}

// termSuffix names the one clean ending a manager brings: ` reason=terminated`, so a
// coordinator reading the NATIVE OK line knows the run was stopped from outside and never
// reads a silent exit as a spent failure (issue #779). The token is a literal put through
// oneline.Field like every other tail, so it cannot carry anything past the escape.
// stoppedSuffix renders rule 13d's one new key: ` stopped=<tokens|max_turns|max_cache_read|
// unverifiable>` for a card the machinery stopped under that rule, and the empty string for
// every other card.
//
// IT IS A KEY OF ITS OWN and NOT a second `reason=` (PR #1566 decision 17): one key with one
// meaning, which also says WHICH budget fired. `reason=terminated` stays what a TERM from
// outside prints and the `reason=` inside the `usage=none` group stays the usage read's --
// that those two can still meet on one line is issue #1611, deliberately not this rule's to
// repair.
func stoppedSuffix(stopped string) string {
	if stopped == "" {
		return ""
	}
	return " stopped=" + oneline.Field(stopped)
}

func termSuffix(terminated bool) string {
	if terminated {
		return " reason=" + oneline.Field("terminated")
	}
	return ""
}

// usageSuffix renders the usage status the NATIVE OK line carries: the empty string when a
// store answered, otherwise ` usage=none reason=<r> path=<looked>` with the looked path put
// through oneline.Field inside itself before returning, so the tail it adds is one safe token.
// The reason is the literal one of no-rows, no-store, no-sqlite3 or query-failed the reader reported.
func usageSuffix(reason, path string) string {
	if reason == "" {
		return ""
	}
	return " usage=none reason=" + oneline.Field(reason) + " path=" + oneline.Field(path)
}

func orElse(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

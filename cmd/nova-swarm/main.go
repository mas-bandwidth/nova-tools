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
//	one line   `native` ends a card on one bounded line, so a coordinator's window never
//	           holds a worker's transcript
//
// EVERYTHING A WORKER WRITES IS DATA. A RESULT.md is a report, never an instruction:
// nothing in it is executed, nothing in it grants anything, and a finding in it is a claim
// to be checked against the repository. That rule is in the spec, where a person reads it,
// and is deliberately nowhere in this code.
package main

import (
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"

	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const usage = `nova-swarm: one-task AI workers, each run in the sandbox with a deadline and a token budget

how it works: a card is one task, a markdown file with a header and its RULES;
a worker description (JSON) names the harness, the model, the key file and the
directories it may read. native runs one card as one child inside nova-sandbox;
member runs a sprint's cards on this machine, each a native child, every sprint
verb sent to the sprint's server; results land under --root. Nothing has a default.
first run: the lines under example: need nothing: a card, a worker description
and the lint's rules; running a card needs a harness, a model's key file and nova-sandbox.

usage:
  nova-swarm version    print this build identity (--version also accepted)
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
  nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
  nova-swarm lint      --card <file> [--typed] [--child-rules | --child-rules-file <file>] [--member-injects] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--trust <file>] [--lineup <file>] [--max <n>] | --fleet <file> [--max <n>] | --rules
                       (--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions)
                       (--child-rules holds the card to the rules the coordinator gives a child: one rule-<name> per required sentence, one step-<what> per forbidden command; the sentences are the built-in general rules, or the lines of --child-rules-file, one required sentence per line; template --name card prints a card that passes the general ones)
                       (--member-injects lints the card as the member stages it, rules by reference: the rules are appended at stage time from the held file of the card's REPO: (fleet/child-rules.txt for nova-tools, fleet/child-rules.<repo>.txt for another), or --child-rules-file; a card need not carry them, and a line that contradicts them is still a finding)
                       (--base-check adds the four checks of a coding card: its PATHS exist at the base sha in --repo (default the working directory), no STEP pushes or calls gh, its LEG is a line of --legs, its deadline is at least --p95's figure for its kind; evidence not given is reported missing, never passed)
                       (nova-sprint add holds a brief to the --child-rules tokens only, and to its model lines: rule-<name> for each rule of its set (the six general rules, or the file add --rules or init --rules names), the step-<what> scans (step-go-clean and step-go-test-timeout only when the file carries those rules), and rule-libraries-considered when the file carries [libraries-considered]; every other token --rules lists is this lint's alone)
  nova-swarm step      --card <file> --dir <checkout> [--work <dir>] [--result <file>] [--sandbox <wall> | --no-wall] | --card <file> --remainder <id> --from <step> --land <sha>
                       (runs the card's own programs: a card whose every work step is a script step, walked in the checkout with no model, each program, POST command and git in its own wall (network denied, no credential, the checkout and a private temp the only writes); one STEP OK|FAILED line per step, stopping at the first failed; refused with no wall unless --no-wall, which runs them unconfined; --remainder prints the card a failed step leaves)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--frame <file>] [--identity <owner>,<name>,<email>]
  nova-swarm member    --as <name> --server <host:port> --harness <path> --root <dir> [--slots <dir>] [--results-root <dir>] [--width <n>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>] [--disk-floor <GiB>] [--stage-wall <duration>] [--identity <owner>,<name>,<email>]
                       (this machine as one member of a sprint's fleet, every sprint verb sent to the sprint's server --server, the run loop nova-sprint run --listen started, so this machine opens no store: beat, queue, push and finish what ended (the child's commit to origin's sprint branch, from outside the wall, never forced; the pull request the child's gh pr create asked for, opened with --gh), each finish judged ok, failed or reaped (docs/SPEC-CARD-CONTRACT.md), take to the width its fleet row names (read with its queue every tick: a member's own row, a reader's its machine's, reader-<m> running at m's width; --width is a twin's override), each card one native child with its frame and an allowlist environment, on the model, budget and deadline its packet's route names (the card decides: the deal draws a route of its tier, or its model: pin; --model, --tokens and --deadline are the override a card with no route runs on); --pass names the secrets a child is handed, the loop record's nova-secrets keys: a loop whose harness reads its provider key from the environment carries --pass <KEY>, else its children start without it and fail at the provider; --reader runs the readers-table loop, each read on the route the ask drew from the reader tier unless --model, --tokens or --deadline is given, and a flash card's first read a decide read, asked by native with JEV_API_KEY from the reader's environment, which no child is handed (docs/SPEC-SPRINT.md section 6); a work card's red gate, its child ended not-done, is classed by native's gate decision with the same key before the take is reported (the failing tests run once at the base, bounded), each decision recorded and shown, and routed only on the sprint row's gate bars, empty by default: flaky failures rerun once, pre-existing ones never the card's (docs/SPEC-SPRINT.md section 5, the gate verdict); --identity names the pool identity every child commits under, from the loop's nova-config argv, else the pool's identity.tsv; a launch it is done with leaves no checkout behind (a failed one keeps its directory, the newest 5 of the pool), and it starts no card while the slots' volume has less free than --disk-floor GiB, default 10; each card's checkout is staged within --stage-wall, default 120s, which a slow machine's loop row names longer; a card it will not start is finished staging refused: <why>, so the sprint deals it to another member and says why)
  nova-swarm disk-guard [--root <dir>]... [--scan <dir>]... [--cache <dir|glob>]... [--cache-max-gb <GiB>] [--modcache-max-gb <GiB>] [--logs <dir>] [--log-max-mb <MiB>] [--log-keep <n>] [--pool-idle <duration>] [--land <dir>] [--clone-age <duration>] [--mirrors <dir>] [--disk-floor <GiB>] [--dry-run]
                       (one pass over this machine, run every few minutes by the disk-guard loop row fleet/loops.yml adds to every machine: every Go build cache (the login's, each root's cache/go-build, each --cache) held under --cache-max-gb, default 10, by the member's trim, oldest entries first and never one used in the last two hours; a module cache over --modcache-max-gb, default 50, emptied while no go command runs; every loop log over --log-max-mb, default 50, copied to <log>.1 and emptied in place, --log-keep copies, default 3; the pool of a loop that stopped (no process names its root, nothing moved for --pool-idle, default 30m) swept as the member sweeps its own, a work launch whose checkout holds commits past its staged one kept; land clones unused for --clone-age, default 24h, removed; a mirror's temporary packs older than an hour removed while nothing fetches into it, never git prune; never anything with uncommitted work or a live process; one REMOVED, TRIMMED, CLEANED, ROTATED or KEPT line per action with freed=<bytes>, a DISK-GUARD WARN line under --disk-floor, default 10, and DISK-GUARD OK freed=<bytes> free=<bytes> at the end; --dry-run judges the same and removes nothing, each action said WOULD-REMOVE, WOULD-TRIM, WOULD-CLEAN or WOULD-ROTATE)
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
absent or empty, a bad invocation; 3 member: its binary was replaced on disk
(MEMBER STOP: its supervisor starts the new one; with children running it first
takes no new card and stops when the last is reported).

NO GUESSED ANYTHING. There is no default pool, no default worker description, no
default number of workers, no default deadline, and no default token budget.
native requires --card; lint takes --card, or
--fleet or --rules instead; verify takes --card as an option and reads it only
when given (because a card this tool chose would be a guess about somebody
else's task); the remaining verbs take no card flag. --tokens is required on
native because a budget this tool supplied would be a guess about somebody
else's task, and --tokens unmetered is a caller's statement that this
provider has no live accounting and the deadline is the only stop. Zero is
refused for tokens. member takes each card's budget from the route its packet
names, and --tokens (with --model and --deadline) only for a card with none.

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

A card to start from: nova-swarm template --name card prints one that passes
the lint (lint --card <file> --child-rules): put it in a file, fill in its
<...> lines (REPO: and BASE: name the repository and the branch the work starts
from and lands on), lint it (a line still unfilled is named on a NOTE line), then
hand it to native, or to nova-sprint add as a brief. native and member each show
one example line in their -h, and template -h lists the lines a card needs.
nova-swarm help <verb> (or <verb> -h) prints one verb's usage, flags and example.

example:
  nova-swarm template --name read-pr
  nova-swarm template --name worker
  nova-swarm lint --rules
`

// verbExamples is the one runnable example line each verb's -h shows, made
// from the verb's own flags: what a stranger copies, then edits. template and
// lint quote theirs from the banner's example block.
var verbExamples = map[string]string{
	"native":     "nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered",
	"step":       "nova-swarm step --card card.md --dir repo",
	"member":     "nova-swarm member --as m1 --server sprint.example:6390 --harness ./harness --root jobs --once",
	"disk-guard": "nova-swarm disk-guard --root ~/nova-bench/run/member --scan ~/nova-bench/run --cache-max-gb 10",
}

// cardLines is what `template -h` lists: the lines a card needs, in the order
// the card template writes them. `lint --rules` says why each is there, and
// every line it lists is one of the template's own (a test holds the two).
const cardLines = `a card's required lines (template --name card writes them; lint --card <file> --child-rules checks them, lint --rules says why each rule is there):
  line 1     RESULT: <label> sha=<sha12>
  REPO:      <owner>/<name>, the repository the member stages and land merges into (land --repo-dir stands in for a card naming none)
  BASE:      <branch>, the branch the work starts from and lands on (land --base stands in for a card naming none)
  a bound    Deadline: finish within <n> minutes.
  RULES.     every rule the coordinator gives a child, each quoted whole
  THE TASK.  what is wanted, the files or package it lives in, the worktree, branch and base
  STEP 1.    one line a step, numbered 1, 2, 3 with no gap; the last step writes RESULT.md, whose line 1 is this card's line 1
  a typed card (lint --typed) also carries KIND:, PATHS:, TEST:, DEPENDS-ON: and DONE-WHEN: lines
`

// verbHelpLines is what a verb's -h shows above its flags beyond the usage it
// quotes: its example line, and for template the card's required lines.
func verbHelpLines(verb string) string {
	add := ""
	if verb == "template" {
		add = cardLines
	}
	if verb == "step" {
		add = "effect: local write: commits in the checkout --dir names, and runs the card's programs in their own wall; --dry-run writes nothing\n"
	}
	if verb == "disk-guard" {
		add = "effect: local write: removes and rotates files on this machine; --dry-run writes nothing\n"
	}
	if ex, ok := verbExamples[verb]; ok {
		add = "example:\n  " + ex + "\n" + add
	}
	return add
}

// verbNames are the verbs, in the usage's order: what a bare command and an unknown verb
// are answered with (the tool-answers rule).
var verbNames = []string{"template", "lint", "member", "native", "step", "disk-guard", "worker", "verify", "doctor", "profile", "slots", "version"}

// helpVerbs are the verbs `help <verb>` answers with that verb's help, the same text
// `<verb> -h` prints.
var helpVerbs = map[string]bool{
	"version": true, "doctor": true, "verify": true, "lint": true,
	"template": true, "profile": true, "native": true, "member": true, "slots": true, "worker": true,
	"step": true, "disk-guard": true,
}

// refuse is what an unusable invocation costs: ONE line, `nova-swarm[ <verb>] REFUSED:
// <what was wrong>; run: <remedy>` (docs/STANDARD.md section 3, point 1), never the banner,
// which is behind `nova-swarm help`. The remedy is the verb's help, or what names its own.
func refuse(stderr io.Writer, where, what string) int {
	run := "; run: nova-swarm help"
	if w := strings.Fields(where); len(w) > 0 && helpVerbs[w[0]] {
		run += " " + w[0]
	}
	if strings.Contains(what, "; run: ") {
		run = ""
	}
	fmt.Fprintf(stderr, "nova-swarm%s REFUSED: %s%s\n", oneline.Escape(where), oneline.Escape(what), oneline.Escape(run))
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
	defer recoverHelp(stdout, &code)
	// --seat <name> (or NOVA_SEAT): the Redis login is read from that seat's
	// file through nova-secrets' library, in this process (#4052).
	args, err := seatcred.FromArgs(args, os.Getenv)
	if err != nil {
		return refuse(stderr, "", err.Error())
	}
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; the verbs are "+strings.Join(verbNames, ", ")+"; `template --name card` is the one that only looks")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		// help <verb> for a named verb; help with a word that is no verb is refused
		// naming the verbs, as an unknown verb is
		if cmd == "help" && len(rest) > 0 && helpVerbs[rest[0]] {
			return run(append(append([]string{}, rest...), "--help"), stdin, stdout, stderr, now)
		}
		if cmd == "help" && len(rest) > 0 && !verbflag.IsHelp(rest[0]) {
			return refuse(stderr, "", fmt.Sprintf("help %q: no such verb", rest[0])+"; the verbs are "+strings.Join(verbNames, ", "))
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	case "doctor":
		return cmdDoctor(rest, stdout, stderr)
	case "verify":
		return cmdVerify(rest, stdout, stderr)
	case "lint":
		return cmdLint(rest, stdout, stderr)
	case "template":
		return cmdTemplate(rest, stdout, stderr)
	case "native":
		return cmdNative(rest, stdout, stderr)
	case "step":
		return cmdStep(rest, stdout, stderr)
	case "member":
		return cmdMember(rest, stdout, stderr, nil)
	case "slots":
		return cmdSlots(rest, stdout, stderr)
	case "disk-guard":
		return cmdDiskGuard(rest, stdout, stderr)
	case "profile":
		return cmdProfile(rest, stdout, stderr)
	case "worker":
		return cmdWorker(rest, stdout, stderr)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %q", cmd)+"; the verbs are "+strings.Join(verbNames, ", "))
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

func newSecondsFlag(fs *flag.FlagSet, name string, def time.Duration, usage string) *secondsFlag {
	v := &secondsFlag{d: def}
	fs.Var(v, name, usage)
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
		// the nearest flag and the verb's flags, never the flag package's line (tool ledger X2)
		refuse(stderr, " "+f.verb, oneline.Cap(verbflag.Explain(f.fs, err), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		refuse(stderr, " "+f.verb, fmt.Sprintf("takes no positional arguments, got %d: %q (every input is a flag)", n, f.fs.Args()))
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

// ratText is a budget's decimal, "" for none.
func ratText(r *big.Rat) string {
	if r == nil {
		return ""
	}
	return cardcost.Text(r)
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

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	f := newFlags("verify")
	result := f.fs.String("result", "", "required: the job's RESULT.md `file`, whose line 1 is checked")
	contract := f.fs.String("contract", "", "required: the card's contract `line`, which line 1 of RESULT.md must equal exactly")
	label := f.fs.String("label", "", "required: the job's `label`, carried on the result line and in the receipt")
	card := f.fs.String("card", "", "the card `file`, read only when given, for the checks that need the card")
	runRecord := f.fs.String("run-record", "", "the job's exit.json `file`: the harness's exit code joins the verdict")
	usageFile := f.fs.String("usage", "", "the job's usage.tsv `file`: tokens, dollars and wall time join the receipt")
	max := f.fs.Int("max", swarm.DefaultContractLines, "the most evidence lines past the disposition, at least 1")
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
		fmt.Fprintf(stderr, "nova-swarm verify: the receipt could not be written: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-swarm verify -h"))
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
	name := f.fs.String("name", "", "required: the template's `name`: "+strings.Join(swarm.TemplateNames(), ", ")+" (card is a whole card that passes lint --child-rules)")
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
		fmt.Fprintf(stderr, "nova-swarm template: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-swarm template -h"))
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
	jobs := f.fs.String("jobs", "", "required: a `glob` of job directories (or timeline.tsv files), each holding a native run's per-turn timeline")
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
	usd             *string
	usageInterval   *secondsFlag
	benchFlag       *string
	stageTimeout    *string
	frame           *string
	identity        *string
	repos           []string
	recipients      []string
}

func nativeFlagSet() (*flags, *nativeFlags) {
	f := newFlags("native")
	// --idle bounds stillness, which is not the bound on length: a card is idle only when
	// neither its own output nor its process tree has moved for this long, so a `go test`
	// printing nothing for minutes is not a dead card. The default is 300s,
	// and 0 turns the watch off.
	//
	// --results-root is where RESULT.md, usage.tsv and the report are published; empty
	// derives <root>/results from the root this run is given. --sweep-now deletes the job
	// directory after that publish, so a bench sweep never deletes the results with the
	// working directory.
	nf := &nativeFlags{
		harness:         f.fs.String("harness", "", "required: the harness binary `path` the child runs under, checked for existence and execution"),
		model:           f.fs.String("model", "", "required without --worker: the `provider/model` to run, one slash, both sides nonempty"),
		cardPath:        f.fs.String("card", "", "required: the card `file`, handed to the child byte for byte as its task"),
		slot:            f.fs.String("slot", "", "required: the slot `dir` this run executes in, under --root; HOME is a data directory beneath it"),
		root:            f.fs.String("root", "", "required: the configured root `dir` the slot sits under; results go under <root>/results"),
		deadline:        f.fs.String("deadline", "", "required: the wall-clock bound that ends the child, a `duration` such as 30m"),
		idle:            f.fs.String("idle", swarm.DefaultNativeIdle.String(), "end the card when neither its output nor its process tree has moved for this `duration`; 0 turns the watch off (default 5m)"),
		label:           f.fs.String("label", "", "the run's `label`, on its NATIVE line and its result (default: the card file's name without its extension)"),
		auth:            f.fs.String("auth", "", "the harness's auth `file`, one entry of it copied into the child's data home (not with a --worker naming a secret)"),
		config:          f.fs.String("config", "", "the harness's provider config `file` (opencode.json), copied beside the auth (not with a --worker naming a secret)"),
		workerFile:      f.fs.String("worker", "", "the worker description `file` (JSON) that names the model, the key and the read roots; nova-swarm worker check checks it"),
		sandbox:         f.fs.String("sandbox", "", "the nova-sandbox binary `path` that builds the wall (default: nova-sandbox on PATH); not with --no-wall"),
		noWall:          f.fs.Bool("no-wall", false, "run the child with no nova-sandbox wall: the caller owns every read and write it makes"),
		noSharedCaches:  f.fs.Bool("no-shared-caches", false, "keep the Go caches under the child's HOME instead of the bench's shared <root>/cache"),
		resultsRootFlag: f.fs.String("results-root", "", "the `dir` RESULT.md, usage.tsv and the report are published under (default <root>/results)"),
		sweepNow:        f.fs.Bool("sweep-now", false, "delete the job directory once its results are published (never before)"),
	}
	// native takes no bench slot lease: a bench's capacity is one place, the dealer's, and a
	// second ledger here would give a second answer. --slots-store and --owner are accepted
	// so a caller that passes them is not refused on an unknown flag, and they are read by
	// nothing.
	_ = f.fs.String("slots-store", "", "accepted and read by nothing: native takes no slot lease (the dealer holds a bench's capacity)")
	_ = f.fs.String("owner", "", "accepted and read by nothing, with --slots-store")
	nf.tokensWord = f.fs.String("tokens", "", "required: the token budget, a number of tokens, or the word unmetered when the provider has no live accounting and the deadline is the only stop (`n|unmetered`)")
	nf.usd = f.fs.String("usd", "", "the dollar budget per card, a decimal such as 0.50: the harness's reported cost at which the card is stopped (stopped=usd), beside --tokens; empty for none")
	nf.usageInterval = newSecondsFlag(f.fs, "usage-interval", swarm.DefaultUsageInterval, "how often the token budget's source is read, a `duration` or whole seconds, at least 1s and under --deadline (default 5s)")
	nf.benchFlag = f.fs.String("bench", "", "this bench's `name`, in a staging timeout's report (default: this machine's host name up to its first dot)")
	nf.stageTimeout = f.fs.String("stage-timeout", "", "the bound on staging the card's checkout from the bench mirror, a `duration` (default 120s)")
	nf.frame = f.fs.String("frame", "", "the frame `file` a member wrote: the repository, commit and branch to stage, from which JOB.md and the shims are written")
	nf.identity = f.fs.String("identity", "", "the pool identity the child commits under, `owner,name,email` (default: <root>/identity.tsv)")
	f.fs.Var(stringListValue{&nf.repos}, "repo", "a repository the card may clone, `owner/name` (again for more): the wall opens the network to it alone")
	f.fs.Var(stringListValue{&nf.recipients}, "recipient", "a bus lane the card may address (again for more); a bus send is denied inside the wall whatever is named")
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
				fmt.Fprintf(stderr, "nova-swarm native: %s\n", oneline.WithRemedy(oneline.Err(problem), "nova-swarm worker check "+*workerFile))
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
	// THE DOLLAR BUDGET (nova-tools #5094), optional: a decimal, refused with the rest.
	var budgetUSD *big.Rat
	if w := strings.TrimSpace(*nf.usd); w != "" {
		r, err := cardcost.Decimal(w)
		switch {
		case err != nil:
			f.add(fmt.Sprintf("--usd wants a dollar budget per card, a decimal like 0.50: %s", oneline.Err(err)))
		case r.Sign() <= 0:
			f.add("--usd is a budget and is above 0; leave it out for no dollar budget")
		default:
			budgetUSD = r
		}
	}
	if f.refused(stderr) {
		return 2
	}
	// CARD-8349: a card budget below the harness's MEASURED startup cost is
	// refused by name before any directory is made and before any child starts.
	if workerGiven && w.HasCardBudget() {
		if sc, err := swarm.ReadStartupCost(*root); err == nil {
			if line := w.BudgetRefusal(sc); line != "" {
				fmt.Fprintln(stderr, oneline.WithRemedy(line, "nova-swarm native -h"))
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
	if reason := swarm.NativeBudgetSourceRefusal(swarm.NativeUsageSource(workerForBudget), budgetTokens, budgetUnmetered, budgetUSD != nil, workerForBudget); reason != "" {
		refuseNative(stderr, reason)
		return 2
	}
	if reason := swarm.NativeUsageIntervalRefusal(usageInterval.d, d); reason != "" {
		fmt.Fprintf(stderr, "nova-swarm native: %s\n", oneline.WithRemedy(reason, "nova-swarm native -h"))
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
	idleDur = min(idleDur, d)
	cardRaw, err := os.ReadFile(*cardPath)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm native: --card wants a readable file: %s\n", oneline.Err(err))
		return 2
	}
	// the pool identity from the loop's argv (nova-config), over <root>/identity.tsv
	var identity *swarm.StagingIdentity
	if *nf.identity != "" {
		id, ierr := swarm.ParseIdentity(*nf.identity)
		if ierr != nil {
			fmt.Fprintf(stderr, "nova-swarm native: %s\n", oneline.WithRemedy(oneline.Err(ierr), "nova-swarm native -h"))
			return 2
		}
		identity = &id
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
	// THE FRAME (docs/SPEC-CARD-CONTRACT.md): a member's launch names the repository, the
	// commit and the branch to stage, and the profile writes JOB.md and the shims from it.
	var frame *cardcontract.Frame
	if *nf.frame != "" {
		fr, ferr := cardcontract.ReadFrame(*nf.frame)
		if ferr != nil {
			fmt.Fprintf(stderr, "nova-swarm native: --frame wants a frame file the member wrote: %s\n", oneline.Err(ferr))
			return 2
		}
		frame = &fr
	}
	cfg := nativeRunConfig{
		frame:          frame,
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
		usd:            budgetUSD,
		unmetered:      budgetUnmetered,
		usageInterval:  usageInterval.d,
		benchName:      *benchFlag,
		stageTimeout:   stageDur,
		identity:       identity,
	}
	if workerGiven {
		cfg.worker = &w
	}
	// CI over work (nova-tools#4293): this run, the wall, the harness and everything
	// the card's child runs, before anything starts. The member that launched it stays
	// at its own priority, so a busy machine still beats.
	if !yieldNative(nativeToCI, stderr) {
		return 2
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
	if res.survivors != "" {
		// what the harness left in its group, ended before this line (nativeEndLeftovers)
		fmt.Fprintf(stdout, " survivors=%s", oneline.Field(res.survivors))
	}
	// what the job spent, by token class, with the harness's own cost (spendWord): the
	// member carries it into the card's cost record (internal/cardcost, ParseSpend)
	if res.spend != "" {
		fmt.Fprintf(stdout, " spend=%s", oneline.Field(res.spend))
	}
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
	// WHICH BUDGET ENDED THE CARD, AND AT WHAT COUNT (nova-tools #5094): the member carries
	// these words into the finish's reason, so the coordinator reads "budget: tokens 509,940
	// of 400,000, $0.03" and not only "budget".
	if res.stopped != "" {
		fmt.Fprintf(stdout, "NATIVE BUDGET label=%s budget: %s\n", oneline.Field(cfg.label),
			oneline.Escape(nativeBudgetWords(res.stopped, cfg.tokens, res.spent, res.partial, cardcost.ParseSpend(res.spend).Actual, ratText(cfg.usd))))
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
//
// A FRAMED CARD'S FINISH IS ITS RESULT (docs/SPEC-CARD-CONTRACT.md). The gh shim records a
// claude child's `gh pr create` or `gh pr review` in <job>/.sprint/finish.md, the child's
// end, and the child writes no RESULT.md; that record, in the contract's shape, is the
// artefact (swarm.FindCardResult reads its shape). The machinery writes no such file, so the
// asked report's exclusion above stands.
func nativeLeftAResult(job string) bool {
	if strings.TrimSpace(job) == "" {
		return false
	}
	if p, ok := swarm.FindCardResult(job); ok && p == filepath.Join(job, cardcontract.FinishName) {
		return true
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
// auto-rejected otherwise. It is the FIELD a reader of the NATIVE line scores the card `fence` by instead
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

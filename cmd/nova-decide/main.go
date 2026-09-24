// nova-decide makes one typed decision per call through TypeSafe Jev.
//
// It reads a questions file and a state text, asks the provider once, and
// prints exactly one line. Exit 0 when every answer is at or above the floor,
// 3 when any answer is below it (a suggestion, never an authorization: the
// caller keeps today's behaviour as the fallback), 2 on refusal (no key, bad
// questions, provider error). The key comes only from the environment and is
// never printed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-decide: one typed decision per call through TypeSafe Jev (see docs/CLI.md)

usage:
  nova-decide --questions <json file> [--state <file>|stdin] [--floor 0.9]
              [--base-url <url>] [--key-env JEV_API_KEY] [--prefix DECIDE]

  nova-decide tune --decisions <jsonl> [--floors 0.5,0.7,0.8,0.9,0.95]
                   [--label <field, default label>] [--choice <field, default decision>]
                   [--conf <field, default confidence>] [--max-escalation 0.7]
                   [--default <answer>] [--observations]

  nova-decide tune --kind <k> [--dsn <dsn>] [--decisions <tsv path>]
                   (the decisions table: refuse a floor with no rows behind it)

  nova-decide tune --propose-floors --log <jsonl> [--registry <path>]
                   [--write <path>] [--floor-for <kind>=<floor>]
                   (a floor PER KIND, the p25 of the provider answers that
                    stood; a floor above what the provider has ever answered
                    for that kind is refused)

  nova-decide route --unit <json file|inline json> --usage <path> --log <path>
                    [--store <host:port> [--user <acl user>]
                     [--store-user <acl user>] [--password-env NOVA_REDIS_BENCH_PASSWORD]
                     [--store-password-env NOVA_REDIS_BENCH_PASSWORD]] [--down-store <host:port>]
                    [--registry <path>] [--floor 0.9] [--base-url <url>]
                    [--key-env JEV_API_KEY]
                    (--usage and --log are REQUIRED whenever jev is asked)
  nova-decide route --unit-id <id> --kind <kind> --no-jev [--files n] [--packages n] [--lanes n]
                    [--lane-owner <lane>] [--attempt rung:outcome:reason] [--platform <name>]
                    [--guard] [--secrets] [--touches guard|secrets|sandbox|sudo|deploy-keys|network]
                    [--fresh-take] [--deadline 45m] [--no-jev]
                    [--card <path> --allowed-routes <path>] [--jev]
  nova-decide route ... [--step-up] [--max-steps 3]
                    (below the floor, re-ask the same question with that rung
                     excluded; every step is a logged decision)

  nova-decide route ... [--paste]
                    (one more line, for a coordinator to act on:
                     ROUTE <unit> -> <mind> (<model id>) conf=<x>)

  nova-decide help --state <json file|inline json>
  nova-decide help [--hours 2] [--retries-on-rung n] [--failures-last-hour n]
                   [--self-inflicted n] [--class-recurring] [--landing-moved]
                   [--uncertainty 0..1] [--asked-all-friends]

  nova-decide log --log <path> --summary [--registry <path>]

  nova-decide review --repo <owner/name> --pr <n> [--card <file>]
                     [--post|--dry-run] [--ledger file|redis|file,redis] [--store <host:port>]
                     [--user <user>] [--password-env <NAME>] [--ledger-path <jsonl>]
                     [--pass-above <n>] [--bounce-below <n>] [--checks <list>]
                     [--base-url <url>] [--key-env <name>] [--gh <path>] [--stream <name>]
                     [--store-user <user>] [--store-password-env <NAME>] [--skip-heads <file>]
                     [--usd-per-mtok-in <x>] [--usd-per-mtok-out <x>]
                     [--no-jev] [--table] [--record <dir>] [--replay <dir>]
  nova-decide review --repo <owner/name> --batch <file of pull request numbers>
                    (the Jev FIRST PASS, nova-tools #2565: mechanical checks in
                     Go with no model -- donewhen, selfcheck, paths, claims --
                     then ONE typed Jev question for a 1-10 score. The line starts
                     JEV and its verdict is PASS, BOUNCE or UNSURE. ci is off
                     unless --checks names it; when it is on, a red or missing
                     ci-ok at the exact head BOUNCEs and names the failing jobs
                     (#2704). It NEVER lands anything.
                     --dry-run is the default; --post is the only write.)

  nova-decide classify --question <q> --evidence <file|-> --pointer <id>
                       [--version 1] [--decider rules] [--floor <f>] [--rules <tsv>] [--tamper <file>]
                       [--escalate-to <name>] [--log <path>] [--private]

  nova-decide outcome --log <path> --unit-id <id> --result green|red|blocked|skipped [--of-time <RFC3339>]
                     (what HAPPENED to a unit a decision routed; the kind and
                      the rung are read from that decision, never retyped)

  --questions <file>  JSON object of name to question: {"type": "choice"|"score"|"noul",
                      "instructions": <text>, "criteria": {<option>: <description>} for
                      choice, [<level texts>] for score, absent for noul} (required;
                      {"questions": {...}} also accepted). The envelope may also
                      carry criteria_version, criteria_file, state_fields and
                      machinery: "machinery": "who-reads" answers the question
                      under internal/decide/readers.go's rules, where a settled
                      security designation is taken with NO provider call and
                      the answer is constrained before it is printed or recorded
  --state <file>      state text the decision is about; stdin when absent or "-" (default stdin)
  --floor <f>         confidence floor; answers below it are a suggestion (default 0.9)
  --base-url <url>    Jev endpoint (default https://api.typesafe.ai/v1/systemone)
  --key-env <name>    environment variable holding the key (default JEV_API_KEY,
                      TYPESAFE_API_KEY also accepted); never a file, never argv
  --prefix <word>     first token of the one line printed (default DECIDE)

  --decisions <file>  JSONL decisions log: each row joins the decision, its
                      confidence and the outcome label (rule 8); with --kind it
                      is the TSV fallback of the decisions table
  --kind <k>          read the decisions table for kind k and refuse a floor
                      with no rows behind it (rule 8)
  --dsn <path>        the decisions table: a TSV path; default $NOVA_DSN
  --floors <list>     comma-separated confidence floors to try
  --label <field>     field holding the outcome (default label)
  --choice <field>    field holding the decision (default decision)
  --conf <field>      field holding the confidence (default confidence)
  --max-escalation <f>  escalation-rate cap for the best floor (default 0.7)
  --observations      read a log whose rows say "adjudicated": false. Their
                      labels were joined afterwards and nobody adjudicated
                      them, so a floor tuned from them is tuned from nothing:
                      without this flag such a log is REFUSED (reason
                      not-adjudicated), and with it the arithmetic is printed
                      in full and the closing line is
                      TUNE OBSERVATIONS ... best_floor=none. A row carrying
                      no such field is a log from before the field existed and
                      is read exactly as it always was
  --propose-floors    propose a floor PER KIND from an escalation log: for each
                      kind, the p25 of the provider answers no failure was
                      recorded against. A kind with fewer than 2 such answers
                      is not proposed a floor and keeps the built-in default
  --write <path>      write the proposed floors into a registry at this path,
                      leaving every other field of the file as it was
  --floor-for <k>=<f> override one proposal by hand; repeatable

route: which mind does this unit of work, over the ladder of minds a registry
holds. The answer is the LOWEST rung the evidence supports with confidence that
the first attempt is right; below the floor it steps UP a rung, never down. A
failed attempt re-enters the decision carrying its evidence and the answer is
the next rung, sideways first (same height, another lineage) then up. Two rungs
are chosen by KIND, not height, and by machinery rather than the provider:
security -- a guard, secrets, the sandbox, sudo, deploy keys, the network -- and
a fresh take. Friends first: the DeepSeek rungs take mechanical kinds only.

An attempt that timed out and is not known to have terminated leaves its rung
OCCUPIED: the answer is the same rung with wait=awaiting_termination, and that
is a WAIT, never permission to retry. Only what the provider is told is typed
and enumerated -- buckets and flags, never a unit's id, a lane's spelling, a
platform's name or an attempt's reason, and the rungs it chooses between are
opaque ids rather than any mind's name.

  --unit <file>       the unit's evidence as JSON (inline JSON also accepted):
                      id, kind, files, packages, lanes, lane_owner, attempts
                      (rung, outcome, reason, terminated), platform, guard,
                      secrets, touches, fresh_take, deadline
  --registry <path>   the registry of minds (name, lineage, height, kinds it is
                      designated for, owned lanes, availability, ask); the
                      embedded ladder when absent
  --log <path>        append this decision to the escalation log, JSON lines.
                      REQUIRED when jev is asked, and the sink is opened BEFORE
                      the call, so a log that will not open is a refusal rather
                      than a call with nowhere to record it
  --store <host:port> also write the decision as one decide event on the
                      cards:done stream of the fleet Redis, with decide_log's
                      fields under decide_log's names; the fold keeps it in its
                      decisions table (nova-pulse fold --report). Opened BEFORE
                      the call, like --log
  --user <name>       with --store: the ACL user
  --password-env <NAME>
                      with --store: the environment variable the password
                      arrives in (default NOVA_REDIS_BENCH_PASSWORD), put there
                      by nova-secrets exec --only <NAME>; never on argv
  --usage <path>      append what a provider call spent to this usage TSV, in
                      the fleet's own columns; a failed call is a row too, with
                      its cost unknown (a dash), never a zero. REQUIRED when jev
                      is asked: a call nobody can account for is refused before
                      it is made, never made and then forgotten. usd is priced
                      from the registry's rate table; a model with no rate is a
                      dash and a NOTE naming it, never a guessed price
  --floor <f>         confidence floor; below it the answer steps UP. Absent,
                      the registry's floor for this unit's KIND answers, and
                      the built-in 0.9 only where that kind has no measured
                      row; every line says which, as floor_from=flag|kind|built-in
  --step-up           below the floor, re-ask the SAME question with that rung
                      excluded from the criteria. Every step is a decision of
                      its own: one log row and one usage row each, and the final
                      line carries steps=<n>
  --max-steps <n>     how many decisions --step-up makes before it stops
                      (default 3); it wants --step-up beside it
  --no-jev            answer by the rules alone: no key, no network, deterministic
  --kind <kind>       rebase | stack | fixture-retarget | fleet-chore |
                      fix-with-red-test | new-verb | spec | design | guard |
                      cause-to-find
  --attempt <a>       rung:outcome[:reason]; outcome is ok | failed | timeout |
                      timeout-terminated | abandoned. A bare timeout is a
                      silence; timeout-terminated is the proof it is dead
  --touches <t>       guard | secrets | sandbox | sudo | deploy-keys | network
  --summary           (log) escalations per kind, the regenerated start rung, and
                      coverage=<outcomes>/<decisions> on the closing line

Accounting is not optional. Token spend reporting is an obligation and every
decision is logged, so a route that will call the provider is refused unless it
says where both go. --no-jev makes no call, so there is nothing to account for
and both stay optional.

exit codes: 0 the answer may be acted on, 1 the verb ran and said NOT YET (a
wait: the rung named owns the work and an attempt on it is not known dead), 3
any answer below the floor (a suggestion), 2 refusal: no key, bad questions, bad
evidence, provider error. Only exit 0 is permission to dispatch. tune exits 2
when fewer than 10 labeled rows (a floor with no rows behind it is untuned).

example:
  nova-decide --questions ./questions.json --state ./state.md --floor 0.9
  nova-decide tune --decisions ./decisions.jsonl
  nova-decide route --unit-id card-41 --kind rebase --files 2 --packages 1 --no-jev
  nova-decide route --unit '{"id":"card-41","kind":"rebase","files":2,"packages":1}' --usage ./usage.tsv --log ./decide.jsonl --no-jev
  nova-decide route --unit-id thin --kind new-verb --no-jev --step-up --log ./decide.jsonl
  nova-decide help --hours 3 --retries-on-rung 2 --landing-moved
  nova-decide log --log ./decide.jsonl --summary
  nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD -- nova-decide route --unit-id card-41 --kind rebase --no-jev --log ./decide.jsonl --store 127.0.0.1:6379
`

// version is empty in every ordinary build and is the one override: a release
// stamps it with -ldflags "-X main.version=<tag>".
var version string

// stdin is a var so tests can replace it; production reads the real stdin.
var stdin io.Reader = os.Stdin

// decisionsOpener opens the decisions table a path names. It is the seam a
// test replaces with a fake driver.
var decisionsOpener = func(dsn string) (decide.DecisionDriver, error) {
	return decide.OpenDecisions(dsn)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "DECIDE REFUSED reason=no-arguments --questions is required, refusing to guess; run: nova-decide help")
		return 2
	}
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version":
			if len(args) > 1 {
				return refuse(stderr, "DECIDE", "bad-flags", fmt.Sprintf("version takes no arguments, got %d", len(args)-1))
			}
			fmt.Fprintln(stdout, buildinfo.Line("nova-decide", version))
			return 0
		case "-h", "--help":
			fmt.Fprint(stdout, usage)
			return 0
		case "help":
			// `nova-decide help` is the door the onboarding standard names, and
			// `nova-decide help --state ...` is the second decision: continue,
			// ask all friends, or ask Glenn.
			if len(args) == 1 {
				fmt.Fprint(stdout, usage)
				return 0
			}
			return runHelp(args[1:], stdout, stderr)
		case "tune":
			return runTune(args[1:], stdout, stderr)
		case "route":
			return runRoute(args[1:], stdout, stderr)
		case "log":
			return runLog(args[1:], stdout, stderr)
		case "classify":
			return runClassify(args[1:], stdout, stderr)
		case "review":
			return runReview(args[1:], stdout, stderr)
		case "outcome":
			return runOutcome(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("nova-decide", flag.ContinueOnError)
	questions := fs.String("questions", "", "JSON file of typed questions (required)")
	stateFile := fs.String("state", "", "file holding the state text; stdin when absent or -")
	floor := fs.Float64("floor", 0.9, "confidence floor; answers below it are a suggestion")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "environment variable holding the key")
	prefix := fs.String("prefix", "DECIDE", "first token of the one line printed")
	dsn := fs.String("dsn", os.Getenv(decide.DecisionsEnv), "the decisions table, a TSV path; records each call")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		return refuse(stderr, "DECIDE", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, *prefix, "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*prefix) == "" {
		return refuse(stderr, "DECIDE", "bad-flags", "--prefix must not be empty")
	}
	if *questions == "" {
		return refuse(stderr, *prefix, "bad-questions", "--questions is required; refusing to guess")
	}
	// NaN compares false against both bounds, so a bare range check gates a
	// decision on a number that is not one. One refusal, with one remedy.
	if err := decide.ValidFloor(*floor); err != nil {
		return refuse(stderr, *prefix, "bad-floor",
			fmt.Sprintf("--floor %v is not a confidence; it wants a number between 0 and 1, such as --floor 0.9", *floor))
	}
	// The question and the criteria it is answered against load as ONE
	// versioned pair, and the pair is what goes out: the criteria are read
	// from the file the question names, beside it and nowhere else, and the
	// state is validated against the typed fields the question declares --
	// all of it BEFORE the provider is dialled, so a missing fact is a
	// refusal and never an answer given over evidence that was not there.
	qf, err := decide.LoadQuestionFile(*questions)
	if err != nil {
		return refuse(stderr, *prefix, "bad-questions", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	qs := qf.Questions
	var state string
	switch {
	case *stateFile == "" || *stateFile == "-":
		b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", fmt.Sprintf("cannot read stdin: %s", oneline.Err(err)))
		}
		state = string(b)
	default:
		if isJSONState(*stateFile) {
			return refuse(stderr, *prefix, "bad-state-format", "state must be key: value lines, not JSON; JSON state is not accepted")
		}
		b, err := os.ReadFile(*stateFile)
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", fmt.Sprintf("cannot read state: %s", oneline.Err(err)))
		}
		state = string(b)
	}
	if isJSONState(state) {
		return refuse(stderr, *prefix, "bad-state-format", "state must be key: value lines, not JSON; JSON state is not accepted")
	}
	payload, err := qf.Payload(state)
	if err != nil {
		return refuse(stderr, *prefix, "bad-state", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	// THE MACHINERY, AT THE CALL BOUNDARY.
	//
	// Where the loaded pair DECLARES the who-reads machinery, the rules in
	// internal/decide/readers.go are the decision and the provider is an
	// advisor constrained by them. A settled security designation is taken
	// HERE -- before a client is constructed, before a key is even wanted, at
	// zero calls and against any confidence -- and every other rule is
	// installed on the client as a constraint that runs over the answer
	// before it is recorded or printed, never after a caller has read it.
	var readState decide.ReadState
	var readDecision decide.ReadDecision
	whoReads := qf.Machinery == decide.MachineryWhoReads
	if whoReads {
		readState, err = decide.ReadStateOf(state)
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	// THE CONFIGURED TABLE IS OPENED BEFORE ANY DECISION IS MADE.
	//
	// It used to be opened after the settled path had already returned, so a
	// decision that cost no call also left no row: the real CLI with a fresh
	// TSV --dsn, a settled state, no key and a dead endpoint printed success
	// and created nothing. A decision the MACHINERY made is the one nobody can
	// reconstruct from a provider's log, so it is exactly the one that is owed
	// a durable row.
	var store decide.DecisionDriver
	if strings.TrimSpace(*dsn) != "" {
		store, err = decisionsOpener(*dsn)
		if err != nil {
			return refuse(stderr, *prefix, "bad-decisions", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		defer store.Close()
	}
	if whoReads {
		if _, _, settled := decide.MandatoryReader(readState); settled {
			d, err := decide.ConstrainRead("", 0, readState)
			if err != nil {
				return refuse(stderr, *prefix, "bad-state", oneline.Cap(err.Error(), oneline.TailBytes))
			}
			// The receipt, and NO fabricated confidence: the provider was not
			// asked, so the row's confidence column is a dash and its source
			// says the machinery decided.
			receipt := decide.ReceiptNotConfigured
			if store != nil {
				q := qs[decide.ReadQuestion]
				if err := store.Append(decide.DecisionRow{
					QuestionHash: decide.QuestionHash(payload, decide.ReadQuestion, q),
					Kind:         q.Kind(),
					Answer:       string(d.First),
					Floor:        *floor,
					Source:       decide.SourceMachinery,
				}); err != nil {
					return refuseWrite(stderr, *prefix, "the decision was settled", err)
				}
				receipt = decide.ReceiptRecorded
			}
			fmt.Fprintln(stdout, decide.ReadLine(*prefix, d, readState, 0, false, *floor, receipt))
			return 0
		}
	}
	client, err := decide.New(*baseURL, *keyEnv)
	if err != nil {
		return refuse(stderr, *prefix, "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	client.SetFloor(*floor)
	if store != nil {
		client.UseDecisions(store)
	}
	if whoReads {
		client.Constrain(func(answers map[string]decide.Answer) (map[string]decide.Answer, error) {
			a := answers[decide.ReadQuestion]
			d, err := decide.ConstrainRead(decide.Role(a.Choice), a.Confidence, readState)
			if err != nil {
				return nil, err
			}
			readDecision = d
			a.Choice = string(d.First)
			answers[decide.ReadQuestion] = a
			// A rule that overrode the answer settled the decision, so the row
			// carries the machinery's source and no provider confidence even
			// though a call was made: the number that came back is not
			// evidence about the answer that stands.
			client.SetRowSource(d.Source, d.Source == decide.SourceProvider)
			return answers, nil
		})
	}
	answers, _, err := client.Decide(context.Background(), payload, qs)
	if err != nil {
		return refuse(stderr, *prefix, "provider-error", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	// A configured table that refused the row is reported, never swallowed: a
	// caller told the decision succeeded while nothing was written has no
	// receipt at all.
	if err := client.RecordErr(); err != nil {
		return refuseWrite(stderr, *prefix, "the answer stands", err)
	}
	if whoReads {
		hasConfidence := readDecision.Source == decide.SourceProvider
		conf := answers[decide.ReadQuestion].Confidence
		receipt := decide.ReceiptNotConfigured
		if store != nil {
			receipt = decide.ReceiptRecorded
		}
		fmt.Fprintln(stdout, decide.ReadLine(*prefix, readDecision, readState, conf, hasConfidence, *floor, receipt))
		if hasConfidence && conf < *floor {
			return 3
		}
		return 0
	}
	fmt.Fprintln(stdout, decide.Line(*prefix, answers, *floor))
	for _, a := range answers {
		if a.Confidence < *floor {
			return 3
		}
	}
	return 0
}

// runTune is the tune verb: it reads a decisions log and prints, per floor,
// how many decisions agreed and how many were escalated, then the best floor
// under --max-escalation. Fewer than decide.MinLabeled labeled rows is a
// refusal: a floor with no rows behind it is untuned.
func runTune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide tune", flag.ContinueOnError)
	decisions := fs.String("decisions", "", "JSONL decisions log; with --kind, the TSV fallback path (rule 8)")
	kind := fs.String("kind", "", "read the decisions table for this kind and refuse a floor with no rows behind it")
	dsn := fs.String("dsn", os.Getenv(decide.DecisionsEnv), "the decisions table, a TSV path; default $NOVA_DSN")
	floors := fs.String("floors", "0.5,0.7,0.8,0.9,0.95", "comma-separated confidence floors to try")
	label := fs.String("label", "label", "field holding the outcome")
	choice := fs.String("choice", "decision", "field holding the decision")
	conf := fs.String("conf", "confidence", "field holding the confidence")
	dflt := fs.String("default", "", "the answer a below-floor row actually gets; with it each floor reports what that default got right and what it MISSED, and the best floor is the one that misses fewest")
	observations := fs.Bool("observations", false, "read a log whose rows say adjudicated:false: the arithmetic runs and NO floor is recommended from it")
	maxEscalation := fs.Float64("max-escalation", decide.DefaultMaxEscalation, "escalation-rate cap for the best floor")
	proposeFloors := fs.Bool("propose-floors", false, "propose a floor PER KIND from an escalation log: the p25 of the provider answers that stood")
	logPath := fs.String("log", "", "the escalation log --propose-floors reads (JSON lines)")
	registry := fs.String("registry", "", "the registry the proposal compares against; the embedded ladder when absent")
	write := fs.String("write", "", "write the proposed floors into a registry at this path, leaving every other field of the file as it was")
	floorFor := &floorOverrides{}
	fs.Var(floorFor, "floor-for", "override one proposal as kind=floor, such as new-verb=0.75; repeatable, and refused above what the provider has ever answered for that kind")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	observeVerbFlags("tune", fs)
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "tune") {
			return 0
		}
		return refuse(stderr, "TUNE", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "TUNE", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *proposeFloors {
		return runProposeFloors(*logPath, *registry, *write, floorFor.values, stdout, stderr)
	}
	if len(floorFor.values) > 0 {
		return refuse(stderr, "TUNE", "bad-flags", "--floor-for has nothing to override without --propose-floors; pass --propose-floors --log <path>")
	}
	if strings.TrimSpace(*kind) != "" {
		return runTuneTable(*kind, *dsn, *decisions, stdout, stderr)
	}
	if strings.TrimSpace(*decisions) == "" {
		return refuse(stderr, "TUNE", "bad-arguments", "--decisions is required; refusing to guess")
	}
	if *maxEscalation < 0 || *maxEscalation > 1 {
		return refuse(stderr, "TUNE", "bad-flags", fmt.Sprintf("--max-escalation must be between 0 and 1 (got %g)", *maxEscalation))
	}
	parsedFloors, err := parseFloors(*floors)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	raw, err := os.ReadFile(*decisions)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-decisions", fmt.Sprintf("cannot read decisions: %s", oneline.Err(err)))
	}
	res, err := decide.Tune(raw, decide.TuneOptions{
		Label:         *label,
		Choice:        *choice,
		Conf:          *conf,
		Floors:        parsedFloors,
		MaxEscalation: *maxEscalation,
		Default:       *dflt,
		Observations:  *observations,
	})
	if err != nil {
		// A log of observations gets its own one-word reason: it is not a bad
		// log, it is a log that cannot set a floor.
		reason := "bad-decisions"
		switch {
		case errors.Is(err, decide.ErrAdjudicatedMalformed):
			// A marker nobody can read is its own fault, and no flag admits
			// it: --observations takes a log that says it is observations.
			reason = "adjudicated-malformed"
		case errors.Is(err, decide.ErrNotAdjudicated):
			reason = "not-adjudicated"
		}
		return refuse(stderr, "TUNE", reason, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if res.Labeled < decide.MinLabeled {
		return refuse(stderr, "TUNE", "too-few-labeled",
			fmt.Sprintf("need at least %d labeled lines, got %d; refusing to set a floor from no data", decide.MinLabeled, res.Labeled))
	}
	fmt.Fprint(stdout, res.Render())
	return 0
}

// floorOverrides is --floor-for: kind=floor, repeatable, in the order given. A
// person overriding a proposal is the case the observed-maximum refusal exists
// for, so the parse is strict and the check comes after.
type floorOverrides struct {
	values []decide.KindFloor
}

func (o *floorOverrides) String() string {
	parts := make([]string, 0, len(o.values))
	for _, f := range o.values {
		parts = append(parts, fmt.Sprintf("%s=%g", f.Kind, f.Floor))
	}
	return strings.Join(parts, ",")
}

func (o *floorOverrides) Set(v string) error {
	kind, value, ok := strings.Cut(v, "=")
	kind = strings.TrimSpace(kind)
	if !ok || kind == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("--floor-for %s wants kind=floor, such as new-verb=0.75", oneline.Field(v))
	}
	if !decide.KnownKind(kind) {
		return fmt.Errorf("--floor-for names kind %s, which is not one of %s", oneline.Field(kind), strings.Join(decide.Kinds, ", "))
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fmt.Errorf("--floor-for %s has a floor that is not a number", oneline.Field(kind))
	}
	if err := decide.ValidFloor(f); err != nil {
		return fmt.Errorf("--floor-for %s wants a number between 0 and 1, such as %s=0.75", oneline.Field(kind), oneline.Field(kind))
	}
	for i, have := range o.values {
		if have.Kind == kind {
			o.values[i].Floor = f
			return nil
		}
	}
	o.values = append(o.values, decide.KindFloor{Kind: kind, Floor: f, From: "named by hand with --floor-for"})
	return nil
}

// runProposeFloors is the per-kind floor proposal: read the escalation log,
// group the PROVIDER answers by kind, keep the ones no failure was recorded
// against, and propose their p25 -- a floor three answers in four would have
// cleared. A kind with too few answers is not proposed a floor and keeps the
// built-in default, and the line says so rather than leaving a reader to infer
// it from a missing row.
//
// A floor above the provider's observed maximum for its kind is REFUSED with
// the remedy, because such a floor cannot gate a decision, only delete it: that
// is the 0.90-against-0.78 defect of 2026-09-18, and it does not get written
// back into the file it came from.
func runProposeFloors(logPath, registryPath, writePath string, overrides []decide.KindFloor, stdout, stderr io.Writer) int {
	if strings.TrimSpace(logPath) == "" {
		return refuse(stderr, "TUNE", "bad-arguments",
			"--propose-floors reads an escalation log; pass --log ./decide.jsonl, because a floor with no rows behind it is untuned")
	}
	reg, err := decide.LoadRegistry(registryPath)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-registry", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	entries, err := decide.ReadEntries(logPath)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	proposals, err := decide.ProposeFloors(reg, entries)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	floors := merged(proposals.Proposed(proposedFrom(logPath)), overrides)
	if err := decide.CheckFloors(floors, proposals); err != nil {
		return refuse(stderr, "TUNE", "floor-above-observed", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprint(stdout, proposals.Render())
	written := "-"
	if strings.TrimSpace(writePath) != "" {
		if len(floors) == 0 {
			return refuse(stderr, "TUNE", "no-floors",
				"no kind has enough provider answers to propose a floor from, so there is nothing to write; route more units, or name one by hand with --floor-for")
		}
		source, err := registrySource(registryPath)
		if err != nil {
			return refuse(stderr, "TUNE", "bad-registry", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		out, err := decide.MergeFloors(source, floors)
		if err != nil {
			return refuse(stderr, "TUNE", "bad-registry", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		if err := os.WriteFile(writePath, out, 0o644); err != nil {
			return refuse(stderr, "TUNE", "bad-registry", fmt.Sprintf("cannot write the registry: %s", oneline.Err(err)))
		}
		written = writePath
	}
	fmt.Fprintf(stdout, "TUNE FLOORS WRITTEN floors=%d path=%s\n", len(floors), oneline.Field(written))
	return 0
}

// merged puts the hand-named floors over the proposed ones, keeping kind order
// stable: a proposal replaced in place, an override for a kind with no proposal
// appended.
func merged(proposed, overrides []decide.KindFloor) []decide.KindFloor {
	out := append([]decide.KindFloor(nil), proposed...)
	for _, o := range overrides {
		replaced := false
		for i := range out {
			if out[i].Kind == o.Kind {
				out[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

// proposedFrom names the measurement in the registry row, so a floor can always
// be traced back to the log it was read off.
func proposedFrom(logPath string) string {
	return fmt.Sprintf("%s, %s", filepath.Base(logPath), now().UTC().Format("2006-01-02"))
}

// registrySource is the bytes the floors are merged into: the file where one
// was named, and the embedded ladder where none was -- so `--write` works on a
// bench that has never had a registry file of its own.
func registrySource(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return decide.DefaultRegistryJSON(), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the registry %s: %w", path, err)
	}
	return raw, nil
}

// runTuneTable is the decisions-table read: it opens the table, reads the rows
// for one kind, and refuses (exit 2, naming the kind) when none exist -- a
// floor with no rows behind it is untuned (rule 8). With rows it reports each
// row's confidence and the outcome that followed, the join rule 8 owes.
func runTuneTable(kind, dsn, decisions string, stdout, stderr io.Writer) int {
	source := strings.TrimSpace(dsn)
	if source == "" {
		source = strings.TrimSpace(decisions)
	}
	store, err := decisionsOpener(source)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-decisions", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	defer store.Close()
	rows, err := store.Rows(kind)
	if err != nil {
		return refuse(stderr, "TUNE", "bad-decisions", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if len(rows) == 0 {
		return refuse(stderr, "TUNE", "no-rows",
			fmt.Sprintf("kind %s has no decisions rows; a floor with no rows behind it is untuned, refusing to guess", oneline.Field(kind)))
	}
	fmt.Fprintf(stdout, "TUNE kind=%s rows=%d\n", oneline.Field(kind), len(rows))
	for _, row := range rows {
		// A row no provider answered carries a DASH, never a number: the
		// machinery settled it, and a zero printed here would read as a
		// measured confidence of zero.
		confidence := "-"
		if row.HasProviderConfidence {
			confidence = fmt.Sprintf("%.2f", row.ProviderConfidence)
		}
		fmt.Fprintf(stdout, "TUNE ROW question_hash=%s answer=%s provider_confidence=%s floor=%.2f outcome=%s source=%s\n",
			oneline.Field(row.QuestionHash), oneline.Field(row.Answer), confidence, row.Floor, orDash(row.Outcome), orDash(row.Source))
	}
	fmt.Fprintf(stdout, "TUNE OK kind=%s rows=%d\n", oneline.Field(kind), len(rows))
	return 0
}

// orDash renders an empty outcome as "-", so the row's last field is never an
// empty token.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return oneline.Field(s)
}

// parseFloors reads a comma-separated floor list, each between 0 and 1. An
// empty list is no floors, which lets decide.Tune apply its default.
func parseFloors(list string) ([]float64, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	parts := strings.Split(list, ",")
	out := make([]float64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty floor in %q", list)
		}
		f, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return nil, fmt.Errorf("bad floor %q", part)
		}
		if err := decide.ValidFloor(f); err != nil {
			return nil, fmt.Errorf("--floors %v is not a confidence; each wants a number between 0 and 1, such as 0.9", f)
		}
		out = append(out, f)
	}
	return out, nil
}

// refuseWrite is the refusal a configured decisions table earns by not taking
// the row. Nothing is printed on stdout: a decision whose row was refused has
// no receipt, and a line followed by a warning is not compatibility.
func refuseWrite(stderr io.Writer, prefix, what string, err error) int {
	return refuse(stderr, prefix, "decisions-write-failed",
		fmt.Sprintf("%s and its configured decisions table refused the row, so there is no receipt: %s", what, oneline.Err(err)))
}

// refuse prints the one refusal line: the prefix, REFUSED, a one-word reason
// and the detail. It goes to stderr; the key is never printed.
func refuse(stderr io.Writer, prefix, reason, detail string) int {
	fmt.Fprintf(stderr, "%s REFUSED reason=%s %s; run: nova-decide help\n", oneline.Field(prefix), oneline.Field(reason), oneline.Escape(oneline.Cap(detail, oneline.TailBytes)))
	return 2
}

// isJSONState reports whether a state string looks like JSON (starts with '{'
// or '[', or is JSON-shaped) rather than key: value lines.
func isJSONState(s string) bool {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return true
	}
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "//") {
			continue
		}
		return strings.HasPrefix(l, "{") || strings.HasPrefix(l, "[")
	}
	return false
}

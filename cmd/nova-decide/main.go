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
	"flag"
	"fmt"
	"io"
	"os"
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

  nova-decide tune --kind <k> [--dsn <dsn>] [--decisions <tsv path>]
                   (the decisions table: refuse a floor with no rows behind it)

  nova-decide route --unit <json file|inline json> --usage <path> --log <path>
                    [--registry <path>] [--floor 0.9] [--base-url <url>]
                    [--key-env JEV_API_KEY]
                    (--usage and --log are REQUIRED whenever jev is asked)
  nova-decide route --unit-id <id> --kind <kind> --no-jev [--files n] [--packages n] [--lanes n]
                    [--lane-owner <lane>] [--attempt rung:outcome:reason] [--platform <name>]
                    [--guard] [--secrets] [--touches guard|secrets|sandbox|sudo|deploy-keys|network]
                    [--fresh-take] [--deadline 45m] [--no-jev]
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

  nova-decide outcome --log <path> --unit-id <id> --result green|red|blocked|skipped
                     (what HAPPENED to a unit a decision routed; the kind and
                      the rung are read from that decision, never retyped)

  --questions <file>  JSON object of name to question: {"type": "choice"|"score"|"noul",
                      "instructions": <text>, "criteria": {<option>: <description>} for
                      choice, [<level texts>] for score, absent for noul} (required;
                      {"questions": {...}} also accepted)
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
  --dsn <dsn>         decisions table DSN (a postgres:// URL, or a TSV path);
                      default $NOVA_DSN
  --floors <list>     comma-separated confidence floors to try
  --label <field>     field holding the outcome (default label)
  --choice <field>    field holding the decision (default decision)
  --conf <field>      field holding the confidence (default confidence)
  --max-escalation <f>  escalation-rate cap for the best floor (default 0.7)

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
  --log <path>        append this decision to the escalation log (JSON lines);
                      REQUIRED when jev is asked
  --usage <path>      append what a provider call spent to this usage TSV, in
                      the fleet's own columns; a failed call is a row too, with
                      its cost unknown (a dash), never a zero. REQUIRED when jev
                      is asked: a call nobody can account for is refused before
                      it is made, never made and then forgotten
  --floor <f>         confidence floor; below it the answer steps UP (default 0.9)
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
`

// version is empty in every ordinary build and is the one override: a release
// stamps it with -ldflags "-X main.version=<tag>".
var version string

// stdin is a var so tests can replace it; production reads the real stdin.
var stdin io.Reader = os.Stdin

// decisionsOpener opens the decisions table a DSN names. It is the seam a test
// replaces with a fake driver, so no test needs a Postgres on a bench.
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
	dsn := fs.String("dsn", os.Getenv(decide.DecisionsEnv), "decisions table DSN or TSV path; records each call")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
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
	raw, err := os.ReadFile(*questions)
	if err != nil {
		return refuse(stderr, *prefix, "bad-questions", fmt.Sprintf("cannot read questions: %s", oneline.Err(err)))
	}
	qs, err := decide.ParseQuestions(raw)
	if err != nil {
		return refuse(stderr, *prefix, "bad-questions", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	var state string
	switch {
	case *stateFile == "" || *stateFile == "-":
		b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", fmt.Sprintf("cannot read stdin: %s", oneline.Err(err)))
		}
		state = string(b)
	default:
		b, err := os.ReadFile(*stateFile)
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", fmt.Sprintf("cannot read state: %s", oneline.Err(err)))
		}
		state = string(b)
	}
	client, err := decide.New(*baseURL, *keyEnv)
	if err != nil {
		return refuse(stderr, *prefix, "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	client.SetFloor(*floor)
	if strings.TrimSpace(*dsn) != "" {
		store, err := decisionsOpener(*dsn)
		if err != nil {
			return refuse(stderr, *prefix, "bad-decisions", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		defer store.Close()
		client.UseDecisions(store)
	}
	answers, _, err := client.Decide(context.Background(), state, qs)
	if err != nil {
		return refuse(stderr, *prefix, "provider-error", oneline.Cap(err.Error(), oneline.TailBytes))
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
	dsn := fs.String("dsn", os.Getenv(decide.DecisionsEnv), "decisions table DSN; default $NOVA_DSN")
	floors := fs.String("floors", "0.5,0.7,0.8,0.9,0.95", "comma-separated confidence floors to try")
	label := fs.String("label", "label", "field holding the outcome")
	choice := fs.String("choice", "decision", "field holding the decision")
	conf := fs.String("conf", "confidence", "field holding the confidence")
	maxEscalation := fs.Float64("max-escalation", decide.DefaultMaxEscalation, "escalation-rate cap for the best floor")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "TUNE", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "TUNE", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
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
	})
	if err != nil {
		return refuse(stderr, "TUNE", "bad-decisions", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if res.Labeled < decide.MinLabeled {
		return refuse(stderr, "TUNE", "too-few-labeled",
			fmt.Sprintf("need at least %d labeled lines, got %d; refusing to set a floor from no data", decide.MinLabeled, res.Labeled))
	}
	fmt.Fprint(stdout, res.Render())
	return 0
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
		fmt.Fprintf(stdout, "TUNE ROW question_hash=%s answer=%s provider_confidence=%.2f floor=%.2f outcome=%s\n",
			oneline.Field(row.QuestionHash), oneline.Field(row.Answer), row.ProviderConfidence, row.Floor, orDash(row.Outcome))
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

// refuse prints the one refusal line: the prefix, REFUSED, a one-word reason
// and the detail. It goes to stderr; the key is never printed.
func refuse(stderr io.Writer, prefix, reason, detail string) int {
	fmt.Fprintf(stderr, "%s REFUSED reason=%s %s; run: nova-decide help\n", oneline.Field(prefix), oneline.Field(reason), oneline.Escape(oneline.Cap(detail, oneline.TailBytes)))
	return 2
}

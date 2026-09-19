// The ladder verbs: route, help and log (Glenn 2026-09-18).
//
//	route  which mind does this unit of work -- the lowest rung the evidence
//	       supports, stepping UP a rung below the floor and never down, with the
//	       two kind designations (security, a fresh take) decided by machinery.
//	help   continue, ask all friends, or ask Glenn -- and Glenn only after the
//	       friends.
//	log    read the escalation log back: the escalations per kind and the
//	       starting rung regenerated from the rows.
//
// Every path is a flag and nothing is read relative to a working directory. The
// Jev key reaches the process only as the environment variable nova-secrets
// exec set; no verb here takes a key on argv, and none prints one. With
// --no-jev the answer is the rules alone: no key, no network, deterministic, so
// the loop runs on a bench with no API at all.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// deciderOpener opens the typed-decision client. It is the seam a test replaces
// with a fake, so no unit test dials the provider or needs a key on disk.
var deciderOpener = func(baseURL, keyEnv string) (decide.Decider, error) {
	client, err := decide.New(baseURL, keyEnv)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// now is a var so a test can pin the log's timestamp.
var now = time.Now

// stringList is a flag that may be given more than once, in order.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// runRoute is the route verb: it builds the evidence, asks for a rung, applies
// the floor, prints one line and appends one log row.
func runRoute(args []string, stdout, stderr io.Writer) int {
	// wall_ms is the verb's start to its line: the clock starts here, on the
	// first line of the verb, and every decision this run prints or persists
	// is stamped with the milliseconds to it.
	verbStart := now()
	fs := flag.NewFlagSet("nova-decide route", flag.ContinueOnError)
	unitPath := fs.String("unit", "", "a JSON file (or inline JSON) holding the unit of work's evidence")
	registry := fs.String("registry", "", "the registry of minds; the embedded ladder when absent")
	logPath := fs.String("log", "", "append the decision to this log (JSON lines)")
	usagePath := fs.String("usage", "", "append what a provider call spent to this usage TSV, in the fleet's own columns")
	floor := fs.Float64("floor", decide.DefaultFloor, "confidence floor; below it the answer steps UP a rung")
	stepUp := fs.Bool("step-up", false, "below the floor, re-ask the same question with that rung excluded from the criteria; every step is a logged decision")
	maxSteps := fs.Int("max-steps", decide.DefaultMaxSteps, "how many decisions --step-up makes before it stops")
	paste := fs.Bool("paste", false, "print one more line the coordinator pastes: ROUTE <unit> -> <mind> (<model id>) conf=<x>")
	useJev := fs.Bool("jev", true, "ask Jev among the eligible rungs")
	noJev := fs.Bool("no-jev", false, "answer by the rules alone: no key, no network, deterministic")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "environment variable holding the key; never a file, never argv")
	id := fs.String("unit-id", "", "the unit's id: the evidence pointer every line carries")
	kind := fs.String("kind", "", "the unit's kind: "+strings.Join(decide.Kinds, " | "))
	files := fs.Int("files", 0, "size: files touched")
	packages := fs.Int("packages", 0, "size: packages touched")
	lanes := fs.Int("lanes", 0, "size: lanes touched")
	laneOwner := fs.String("lane-owner", "", "the lane this unit belongs to, whose owner is preferred at equal height")
	platform := fs.String("platform", "", "a platform need, such as windows")
	guard := fs.Bool("guard", false, "a guard is touched")
	secrets := fs.Bool("secrets", false, "secrets are touched")
	freshTake := fs.Bool("fresh-take", false, "this wants a fresh take: a design with one author")
	deadline := fs.String("deadline", "", "the deadline, as a duration such as 45m")
	attempts := &stringList{}
	fs.Var(attempts, "attempt", "a prior attempt as rung:outcome[:reason]; outcome is "+strings.Join(attemptOutcomes(), " | ")+"; repeatable, in order")
	touches := &stringList{}
	fs.Var(touches, "touches", "security this unit touches: "+strings.Join(decide.Touches, " | ")+"; repeatable")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "ROUTE", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "ROUTE", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// The floor is refused here, with the one remedy, before anything else
	// reads it: NaN compares false against every bound, so a bare range check
	// lets it through and a decision is gated on a number that is not one.
	if err := decide.ValidFloor(*floor); err != nil {
		return refuse(stderr, "ROUTE", "bad-floor",
			fmt.Sprintf("--floor %v is not a confidence; it wants a number between 0 and 1, such as --floor 0.9", *floor))
	}
	// The step count is a count, and it means something only where there is a
	// step to take: a --max-steps on its own is a caller who thinks they asked
	// for the step-up and did not, which is worse than a refusal.
	if set["max-steps"] && !*stepUp {
		return refuse(stderr, "ROUTE", "bad-flags",
			fmt.Sprintf("--max-steps %d has nothing to cap without --step-up; pass --step-up, or drop --max-steps", *maxSteps))
	}
	if *stepUp && *maxSteps < 1 {
		return refuse(stderr, "ROUTE", "bad-flags",
			fmt.Sprintf("--max-steps %d asks nothing; it wants at least 1, such as --max-steps %d", *maxSteps, decide.DefaultMaxSteps))
	}
	// Accounting is not optional. Token spend reporting is an obligation and
	// every decision is logged (Glenn), so a route that is going to call the
	// provider says where the spend and the decision will be written BEFORE it
	// calls: a call nobody can account for is refused rather than made. With
	// --no-jev there is no call and nothing to account for, so both stay
	// optional there.
	if *useJev && !*noJev {
		var missing []string
		if strings.TrimSpace(*usagePath) == "" {
			missing = append(missing, "--usage")
		}
		if strings.TrimSpace(*logPath) == "" {
			missing = append(missing, "--log")
		}
		if len(missing) > 0 {
			return refuse(stderr, "ROUTE", "no-accounting", fmt.Sprintf(
				"a jev call must be accounted for: %s missing; pass %s, or --no-jev to answer by the rules alone with no call to account for",
				strings.Join(missing, " and "), remedyFor(missing)))
		}
	}
	unit, code := buildUnit(*unitPath, set, stderr, decide.Unit{
		ID: *id, Kind: *kind, Files: *files, Packages: *packages, Lanes: *lanes,
		LaneOwner: *laneOwner, Platform: *platform, Guard: *guard, Secrets: *secrets,
		Touches: *touches, FreshTake: *freshTake, Deadline: *deadline,
	}, *attempts)
	if code != 0 {
		return code
	}
	reg, err := decide.LoadRegistry(*registry)
	if err != nil {
		return refuse(stderr, "ROUTE", "bad-registry", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	ask := *useJev && !*noJev
	var res decide.RouteResult
	var routeErr error
	var steps []decide.RouteResult
	var client decide.Decider
	if ask {
		opened, err := deciderOpener(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, "ROUTE", "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		client = opened
		fmt.Fprintf(stderr, "nova-decide route: asking jev about unit %s (kind %s, floor %.2f)\n", oneline.Field(unit.ID), oneline.Field(unit.Kind), *floor)
	}
	switch {
	case *stepUp:
		// The step-up is a SEQUENCE of decisions, and the caller gets all of
		// them: the last is the answer, and every one of them is a row.
		steps, routeErr = decide.RouteStepUp(context.Background(), client, reg, unit, *floor, *maxSteps)
		if len(steps) > 0 {
			res = steps[len(steps)-1]
		}
	case ask:
		res, routeErr = decide.RouteJev(context.Background(), client, reg, unit, *floor)
	default:
		res, routeErr = decide.RouteRules(reg, unit, *floor)
	}
	if ask {
		fmt.Fprintf(stderr, "nova-decide route: jev answered for unit %s\n", oneline.Field(unit.ID))
	}
	// Stamp the wall clock on every decision the verb prints or persists,
	// answer and refusal alike: wall_ms is the verb's start to its line, and
	// a row with no stamp carries no measurement, never a zero.
	stampWall := func(r *decide.RouteResult) {
		r.WallMs = int(now().Sub(verbStart).Milliseconds())
		r.HasWallMs = true
	}
	stampWall(&res)
	for i := range steps {
		stampWall(&steps[i])
	}
	// The record is written BEFORE the refusal is returned. A call that has
	// already been made has already been paid for, and a decision that could
	// not be made is still evidence: neither is unspent or unmade by an error
	// on the way out (Stella, #1327). The exit code stays the refusal's own.
	// Every step is a logged decision: the rows go down in the order they were
	// made, and the last one is the answer the line prints.
	var persisted error
	if *stepUp {
		persisted = persistSteps(steps, unit, *logPath, *usagePath)
	} else {
		persisted = persist(res, unit, *logPath, *usagePath)
	}
	if routeErr != nil {
		detail := oneline.Cap(routeErr.Error(), oneline.TailBytes)
		if persisted != nil {
			detail += "; and the record could not be written: " + oneline.Err(persisted)
		}
		return refuse(stderr, "ROUTE", "no-rung", detail)
	}
	if persisted != nil {
		return refuse(stderr, "ROUTE", "bad-record", oneline.Cap(persisted.Error(), oneline.TailBytes))
	}
	fmt.Fprintln(stdout, res.Line())
	// THE COORDINATOR'S LINE (Glenn 2026-09-19). The decision line above is
	// the machine's, with every field a gate needs on it. This one is for a
	// person -- or for the coordinator about to spawn a child -- and it says
	// the one thing they act on: who does this, and on what model. The model
	// id comes from the registry, never from here, and a rung that is ASKED
	// rather than run says so in place of an id it does not have.
	if *paste {
		fmt.Fprintln(stdout, pasteLine(reg, res))
	}
	switch {
	case !res.Dispatchable():
		// The verb ran and said NOT YET. Only exit 0 is permission (SPEC.md),
		// and a wait is not permission: the rung named owns the work, and the
		// caller's next move is to establish what happened to the attempt.
		return 1
	case res.Confidence < *floor:
		return 3
	}
	return 0
}

// persistSteps writes every step of a step-up, in the order they were made. A
// step-up is not one decision with a bigger number on it: it is N decisions,
// each asked, each answered, each paid for, and the record says so -- so the
// log can be read back and the spend adds up whichever step landed.
func persistSteps(steps []decide.RouteResult, u decide.Unit, logPath, usagePath string) error {
	var failures []string
	for i, step := range steps {
		if err := persist(step, u, logPath, usagePath); err != nil {
			failures = append(failures, fmt.Sprintf("step %d: %s", i+1, err))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(failures, "; "))
}

// persist writes the decision's record: the log row for any decision that got
// far enough to be one, and the usage row for any provider call that was
// actually made. It runs on the way out of BOTH paths -- the answer and the
// refusal -- so no row is lost to an error that came after the spend.
func persist(res decide.RouteResult, u decide.Unit, logPath, usagePath string) error {
	if res.Unit == "" {
		return nil // nothing got as far as being a decision
	}
	var failures []string
	if strings.TrimSpace(logPath) != "" {
		if err := decide.AppendEntry(logPath, decide.EntryFor(res, u, now())); err != nil {
			failures = append(failures, "log: "+err.Error())
		}
	}
	// A decision that made no call writes no usage row: an empty row would be a
	// claim that a call was made.
	if strings.TrimSpace(usagePath) != "" && res.Usage.Calls > 0 {
		if err := appendUsage(usagePath, res, u); err != nil {
			failures = append(failures, "usage: "+err.Error())
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(failures, "; "))
}

// appendUsage writes one row of the fleet's usage TSV for the provider call
// this decision made: the same columns, written by the same appender, that
// nova-swarm writes for a card, so nova-tokens reads a decision's spend the way
// it reads everything else. A field the provider did not report is the literal
// "-" and never a 0 (SPEC-TOKENS rule 14), so a failed call's tokens are an
// absence rather than a claim that it was free.
func appendUsage(path string, res decide.RouteResult, u decide.Unit) error {
	row := swarm.UsageRow{
		"job":      u.ID,
		"attempt":  strconv.Itoa(len(u.Attempts) + 1),
		"started":  now().UTC().Format(time.RFC3339),
		"ended":    now().UTC().Format(time.RFC3339),
		"rc":       "0",
		"provider": usageProvider,
		"model":    decide.DefaultModel,
	}
	// Per counter, because the provider reports them per counter: an unreported
	// one is left empty and AppendCardUsage writes it as "-", while a reported
	// zero is written as the measurement it is.
	if res.Usage.HasInput {
		row["tokens_in"] = strconv.Itoa(res.Usage.InputTokens)
	}
	if res.Usage.HasOutput {
		row["tokens_out"] = strconv.Itoa(res.Usage.OutputTokens)
	}
	if res.Usage.Failed {
		row["rc"] = "2"
	}
	return swarm.AppendCardUsage(path, row)
}

// usageProvider is who the tokens were spent with, in the usage file's own
// vocabulary: the Jev endpoint is TypeSafe's.
const usageProvider = "typesafe"

// pasteLine is the one line a coordinator pastes: the unit, the mind that
// answers it, the model id that mind runs on where the registry gives one, and
// the confidence the floor was applied to. A mind with no model id is a mind
// that is ASKED -- a friend, a child, Glenn -- and the line says how, because
// "ask on the bus" is the action, not a model to launch.
func pasteLine(reg *decide.Registry, res decide.RouteResult) string {
	how := "ask-" + res.Rung.Ask
	if model, ok := reg.ModelFor(res.Rung.Name); ok {
		how = model
	}
	return fmt.Sprintf("ROUTE %s -> %s (%s) conf=%.2f",
		oneline.Field(res.Unit), oneline.Field(res.Rung.Name), oneline.Field(how), res.Confidence)
}

// remedyFor is the line a person can paste: the missing accounting flags with a
// path each, rather than the name of a flag they then have to look up.
func remedyFor(missing []string) string {
	out := make([]string, 0, len(missing))
	for _, flag := range missing {
		switch flag {
		case "--usage":
			out = append(out, "--usage ./usage.tsv")
		case "--log":
			out = append(out, "--log ./decide.jsonl")
		default:
			out = append(out, flag)
		}
	}
	return strings.Join(out, " ")
}

// buildUnit reads the evidence: a --unit file (or inline JSON), or the unit
// flags, never both. It returns the unit and 0, or a refusal's exit code.
func buildUnit(path string, set map[string]bool, stderr io.Writer, fromFlags decide.Unit, attempts []string) (decide.Unit, int) {
	unitFlags := []string{"unit-id", "kind", "files", "packages", "lanes", "lane-owner", "platform", "guard", "secrets", "touches", "fresh-take", "deadline", "attempt"}
	given := make([]string, 0, len(unitFlags))
	for _, name := range unitFlags {
		if set[name] {
			given = append(given, "--"+name)
		}
	}
	if set["unit"] && len(given) > 0 {
		return decide.Unit{}, refuse(stderr, "ROUTE", "bad-flags",
			fmt.Sprintf("--unit and %s both name the evidence; give one or the other, refusing to guess which", strings.Join(given, ", ")))
	}
	if set["unit"] {
		raw := []byte(path)
		if !strings.HasPrefix(strings.TrimSpace(path), "{") {
			read, err := os.ReadFile(path)
			if err != nil {
				return decide.Unit{}, refuse(stderr, "ROUTE", "bad-unit", fmt.Sprintf("cannot read the unit: %s", oneline.Err(err)))
			}
			raw = read
		}
		unit, err := decide.ParseUnit(raw)
		if err != nil {
			return decide.Unit{}, refuse(stderr, "ROUTE", "bad-unit", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		return unit, 0
	}
	if len(given) == 0 {
		return decide.Unit{}, refuse(stderr, "ROUTE", "bad-unit", "--unit (or --unit-id and --kind) is required; the evidence is not guessed")
	}
	for _, a := range attempts {
		parts := strings.SplitN(a, ":", 3)
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return decide.Unit{}, refuse(stderr, "ROUTE", "bad-unit",
				fmt.Sprintf("--attempt %s wants rung:outcome[:reason], such as opus:failed:missed the cause", oneline.Field(a)))
		}
		att := decide.Attempt{Rung: strings.TrimSpace(parts[0]), Outcome: strings.TrimSpace(parts[1])}
		// A timeout is a silence, not a death: `timeout` leaves the rung
		// occupied, and `timeout-terminated` is the proof that it is free.
		if att.Outcome == outcomeTimeoutTerminated {
			att.Outcome, att.Terminated = decide.OutcomeTimeout, true
		}
		if len(parts) == 3 {
			att.Reason = strings.TrimSpace(parts[2])
		}
		fromFlags.Attempts = append(fromFlags.Attempts, att)
	}
	return fromFlags, 0
}

// outcomeTimeoutTerminated is the one spelling that carries termination proof
// on the command line, where an attempt has no field of its own.
const outcomeTimeoutTerminated = "timeout-terminated"

// attemptOutcomes is what --attempt accepts, for the flag's own help.
func attemptOutcomes() []string {
	return []string{decide.OutcomeOK, decide.OutcomeFailed, decide.OutcomeTimeout, outcomeTimeoutTerminated, decide.OutcomeAbandoned}
}

// runHelp is the help verb: continue, ask all friends, or ask Glenn.
func runHelp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide help", flag.ContinueOnError)
	statePath := fs.String("state", "", "a JSON file (or inline JSON) holding the help evidence")
	hours := fs.Float64("hours", 0, "hours on the same problem")
	retries := fs.Int("retries-on-rung", 0, "retries on one rung")
	failures := fs.Int("failures-last-hour", 0, "failures in the last hour")
	selfInflicted := fs.Int("self-inflicted", 0, "of those failures, the ones we caused ourselves")
	recurring := fs.Bool("class-recurring", false, "a class of failure is recurring")
	landingMoved := fs.Bool("landing-moved", false, "landing moved")
	uncertainty := fs.Float64("uncertainty", 0, "stated uncertainty, between 0 and 1")
	asked := fs.Bool("asked-all-friends", false, "all friends have already been asked")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "HELP", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "HELP", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	state := decide.HelpState{
		Hours: *hours, RetriesOnRung: *retries, FailuresLastHour: *failures,
		SelfInflicted: *selfInflicted, ClassRecurring: *recurring, LandingMoved: *landingMoved,
		Uncertainty: *uncertainty, AskedAllFriends: *asked,
	}
	if set["state"] {
		raw := []byte(*statePath)
		if !strings.HasPrefix(strings.TrimSpace(*statePath), "{") {
			read, err := os.ReadFile(*statePath)
			if err != nil {
				return refuse(stderr, "HELP", "bad-state", fmt.Sprintf("cannot read the state: %s", oneline.Err(err)))
			}
			raw = read
		}
		parsed, err := decide.ParseHelpState(raw)
		if err != nil {
			return refuse(stderr, "HELP", "bad-state", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		state = parsed
	}
	res, err := decide.Help(state)
	if err != nil {
		return refuse(stderr, "HELP", "bad-state", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintln(stdout, res.Line())
	return 0
}

// runLog is the log verb: the escalation counts per kind and the starting rung
// regenerated from the rows.
func runLog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide log", flag.ContinueOnError)
	logPath := fs.String("log", "", "the escalation log to read (JSON lines)")
	registry := fs.String("registry", "", "the registry of minds; the embedded ladder when absent")
	summary := fs.Bool("summary", false, "print the per-kind escalation counts and the regenerated starting rung")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "LOG", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "LOG", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*logPath) == "" {
		return refuse(stderr, "LOG", "bad-arguments", "--log is required; refusing to guess the log's path")
	}
	if !*summary {
		return refuse(stderr, "LOG", "bad-arguments", "--summary is the read this verb offers; pass it")
	}
	reg, err := decide.LoadRegistry(*registry)
	if err != nil {
		return refuse(stderr, "LOG", "bad-registry", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	entries, err := decide.ReadEntries(*logPath)
	if err != nil {
		return refuse(stderr, "LOG", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	sum, err := decide.Summarize(reg, entries)
	if err != nil {
		return refuse(stderr, "LOG", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprint(stdout, sum.Render())
	return 0
}

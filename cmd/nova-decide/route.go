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
	"errors"
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

// answerHelp is the sub-verb help answer (the #1358 class). Package flag returns
// -h and --help from Parse as flag.ErrHelp, and a flag set whose output is
// io.Discard -- which every verb here uses, because the oneline audit will not
// let package flag print a line the binary did not author -- turned a
// reasonable question into `flag: help requested` at exit 2. It is answered
// here instead: that verb's own lines of the usage block, on stdout, at exit 0.
//
// The lines come out of the usage block and the verb asked about is never
// echoed, so nothing read off the command line reaches a stream through this.
func answerHelp(err error, stdout io.Writer, verb string) bool {
	if !errors.Is(err, flag.ErrHelp) {
		return false
	}
	fmt.Fprint(stdout, verbUsage(usage, verb))
	return true
}

// verbUsage is the lines of a usage block that name one verb: every line
// opening `  nova-decide <verb>`, and the wrapped lines under it, which are
// indented further and carry no verb of their own. A verb the block does not
// name falls back to the whole block rather than to nothing.
func verbUsage(block, verb string) string {
	head := "  nova-decide " + verb
	var out []string
	carry := false
	for _, line := range strings.Split(block, "\n") {
		switch {
		case line == head || strings.HasPrefix(line, head+" "):
			out = append(out, line)
			carry = true
		case carry && strings.HasPrefix(line, "     ") && strings.TrimSpace(line) != "":
			out = append(out, line)
		default:
			carry = false
		}
	}
	if len(out) == 0 {
		return block
	}
	return strings.Join(out, "\n") + "\n"
}

// deciderOpener opens the typed-decision client. It is the seam a test replaces
// with a fake, so no unit test dials the provider or needs a key on disk.
var deciderOpener = func(baseURL, keyEnv string) (decide.Decider, error) {
	client, err := decide.New(baseURL, keyEnv)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// logSinkOpener opens the JSON lines log --log names. It is the seam a test
// replaces with an in-memory sink.
var logSinkOpener = func(path string) (decide.LogSink, error) {
	return decide.OpenLogSink(path)
}

// eventSinkOpener dials the fleet store --store names and returns the sink that
// writes each decision as one decide event on cards:done (#2623). The password
// is read from the environment variable --password-env names, never from argv.
var eventSinkOpener = func(addr, user, password string) (decide.LogSink, error) {
	return decide.OpenEventSink(addr, user, password, "")
}

// downFriendsOpener reads which friends are marked down in the fleet store (#3397).
// It is the seam a test replaces with a fake or miniredis check.
var downFriendsOpener = func(ctx context.Context, addr, user, password string, reg *decide.Registry) (map[string]bool, []string, error) {
	return decide.DownFriends(ctx, addr, user, password, reg)
}

// defaultPasswordEnv is the variable `nova-secrets exec --only
// NOVA_REDIS_BENCH_PASSWORD` leaves the fleet store's password in, the same one
// nova-pulse event reads.
const defaultPasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"

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
	logPath := fs.String("log", "", "append the decision to this JSON lines log")
	store := fs.String("store", "", "the fleet Redis as host:port: write the decision as one decide event on cards:done, which the fold keeps in its decisions table")
	downStore := fs.String("down-store", os.Getenv("NOVA_REDIS_ADDR"), "fleet Redis to read friend presence from without writing an event (default NOVA_REDIS_ADDR; --store also supplies it)")
	var storeUser string
	fs.StringVar(&storeUser, "user", "", "with --store: the ACL user")
	fs.StringVar(&storeUser, "store-user", "", "alias for --user")
	var passwordEnv string
	fs.StringVar(&passwordEnv, "password-env", defaultPasswordEnv, "with --store: the environment variable the password arrives in; never the password itself")
	fs.StringVar(&passwordEnv, "store-password-env", defaultPasswordEnv, "alias for --password-env")
	usagePath := fs.String("usage", "", "append what a provider call spent to this usage TSV, in the fleet's own columns")
	floor := fs.Float64("floor", decide.DefaultFloor, "confidence floor; below it the answer steps UP a rung. Absent, the registry's floor for this unit's KIND answers, and the built-in default only where the kind has none")
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
	cardPath := fs.String("card", "", "the card file: its work type is classified (the rules first, Jev only where no rule fires) and WORKTYPE: and ROUTE: jev= are written onto it")
	allowedPath := fs.String("allowed-routes", "", "with --card: allowed_routes, a JSON object of work type to route list; the selected rung (by name or model id) must be in allowed_routes[type], and a card whose type produces a branch is refused without it")
	attempts := &stringList{}
	fs.Var(attempts, "attempt", "a prior attempt as rung:outcome[:reason]; outcome is "+strings.Join(attemptOutcomes(), " | ")+"; repeatable, in order")
	touches := &stringList{}
	fs.Var(touches, "touches", "security this unit touches: "+strings.Join(decide.Touches, " | ")+"; repeatable")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	observeVerbFlags("route", fs)
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "route") {
			return 0
		}
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
	// The card is read and allowed_routes loaded BEFORE any call: a card that
	// cannot be read or stamped is refused with nothing spent on it.
	var card string
	var allowed decide.WorkTypeRoutes
	if set["allowed-routes"] && !set["card"] {
		return refuse(stderr, "ROUTE", "bad-flags", "--allowed-routes is read for a card's work type; pass --card, or drop --allowed-routes")
	}
	if set["card"] {
		raw, err := os.ReadFile(*cardPath)
		if err != nil {
			return refuse(stderr, "ROUTE", "bad-card", fmt.Sprintf("cannot read the card: %s", oneline.Err(err)))
		}
		card = string(raw)
		if set["allowed-routes"] {
			loaded, err := decide.LoadWorkTypeRoutes(*allowedPath)
			if err != nil {
				return refuse(stderr, "ROUTE", "bad-allowed-routes", oneline.Cap(err.Error(), oneline.TailBytes))
			}
			allowed = loaded
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
	// The floor is PER KIND. One floor for every kind is one number standing in
	// for ten different questions, and on 2026-09-18 it put 13 of 13 provider
	// answers below itself and stepped every one of them up. A --floor given on
	// the command line still wins -- the person asking is looking at something
	// the table does not know -- and every line says which it was.
	effectiveFloor, floorFrom := decide.ResolveFloor(reg, unit.Kind, *floor, set["floor"])
	ask := *useJev && !*noJev
	// The log and the stream are OPENED before the call, not after it. A sink
	// that will not open is nowhere to record the decision: the rule of #1327 is
	// that a jev call with nowhere to record it is refused BEFORE it is made,
	// and a store that does not answer is exactly that case.
	var fileSink, eventSink decide.LogSink
	openFailed := func(err error) int {
		reason := "bad-log"
		if ask {
			reason = "no-accounting"
		}
		return refuse(stderr, "ROUTE", reason, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if strings.TrimSpace(*logPath) != "" {
		opened, err := logSinkOpener(*logPath)
		if err != nil {
			return openFailed(err)
		}
		fileSink = opened
	}
	var downExcluded map[string]bool
	var downList []string
	if strings.TrimSpace(*store) != "" {
		opened, err := eventSinkOpener(*store, storeUser, os.Getenv(passwordEnv))
		if err != nil {
			if fileSink != nil {
				fileSink.Close()
			}
			return openFailed(err)
		}
		eventSink = opened

	}
	presenceStore := strings.TrimSpace(*downStore)
	if strings.TrimSpace(*store) != "" {
		presenceStore = strings.TrimSpace(*store)
	}
	downChecked := presenceStore != ""
	if downChecked {
		down, list, err := downFriendsOpener(context.Background(), presenceStore, storeUser, os.Getenv(passwordEnv), reg)
		if err != nil {
			if fileSink != nil {
				fileSink.Close()
			}
			if eventSink != nil {
				eventSink.Close()
			}
			return openFailed(err)
		}
		downExcluded = down
		downList = list
	} else {
		fmt.Fprintln(stderr, "ROUTE NOTE down friends not checked (no store)")
	}
	var sink decide.LogSink
	if fileSink != nil || eventSink != nil {
		sink = decide.Tee(fileSink, eventSink)
		defer sink.Close()
	}
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
		fmt.Fprintf(stderr, "nova-decide route: asking jev about unit %s (kind %s, floor %.2f from %s)\n",
			oneline.Field(unit.ID), oneline.Field(unit.Kind), effectiveFloor, oneline.Field(floorFrom))
	}
	// THE WORK TYPE ON THE CARD (#2943), classified BEFORE the route is chosen.
	// The rules read the card with no call; Jev is asked only where no rule
	// fires, and that call is accounted for like the route's own. A card whose
	// type produces a branch is refused here, before any route call, when no
	// allowed_routes table was given: for a coding card that table is the gate.
	var wt decide.WorkTypeResult
	if set["card"] {
		var d decide.Decider
		if ask {
			d = client
		}
		classified, err := decide.ClassifyWorkType(context.Background(), d, card)
		if err != nil {
			return refuse(stderr, "ROUTE", "bad-card", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		wt = classified
		if wt.Usage.Calls > 0 && strings.TrimSpace(*usagePath) != "" {
			if err := appendUsage(*usagePath, decide.RouteResult{Unit: unit.ID, Usage: wt.Usage}, unit, reg, stderr); err != nil {
				return refuse(stderr, "ROUTE", "bad-record", "usage: "+oneline.Cap(err.Error(), oneline.TailBytes))
			}
		}
		if err := allowed.RequireTable(wt.Type); err != nil {
			return refuse(stderr, "ROUTE", "no-allowed-routes", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	switch {
	case *stepUp:
		// The step-up is a SEQUENCE of decisions, and the caller gets all of
		// them: the last is the answer, and every one of them is a row.
		steps, routeErr = decide.RouteStepUpExcluded(context.Background(), client, reg, unit, effectiveFloor, *maxSteps, downExcluded, downList)
		if len(steps) > 0 {
			res = steps[len(steps)-1]
		}
	case ask:
		res, routeErr = decide.RouteJevExcluded(context.Background(), client, reg, unit, effectiveFloor, downExcluded, downList)
	default:
		res, routeErr = decide.RouteRulesExcluded(reg, unit, effectiveFloor, downExcluded, downList)
	}
	// Where the floor came from is the decision's own fact, and it travels with
	// it onto the line and into every log row -- including the steps, each of
	// which was gated on the same floor.
	res.FloorFrom = floorFrom
	res.DownChecked = downChecked
	for i := range steps {
		steps[i].FloorFrom = floorFrom
		steps[i].DownChecked = downChecked
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
		persisted = persistSteps(steps, unit, sink, *usagePath, reg, stderr)
	} else {
		persisted = persist(res, unit, sink, *usagePath, reg, stderr)
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
	// THE ALLOWED_ROUTES GATE ON THE SELECTED ROUTE (#2943, Stella's read at
	// afd3efb0). The rung just chosen must be one of allowed_routes[type]; a
	// rung the table forbids is refused and the card is not stamped, so no
	// card carries a stamp for a route it may not run on.
	var cardLines []string
	if set["card"] {
		if err := allowed.Admit(wt.Type, res, reg); err != nil {
			return refuse(stderr, "ROUTE", "route-not-allowed", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		cardLines = decide.WorkTypeCardLines(wt, res, reg, allowed)
		if err := os.WriteFile(*cardPath, []byte(decide.StampCard(card, cardLines)), 0o644); err != nil {
			return refuse(stderr, "ROUTE", "bad-card", fmt.Sprintf("cannot write the card: %s", oneline.Err(err)))
		}
	}
	fmt.Fprintln(stdout, res.Line())
	for _, line := range cardLines {
		fmt.Fprintln(stdout, line)
	}
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
	case res.Confidence < effectiveFloor:
		return 3
	}
	return 0
}

// persistSteps writes every step of a step-up, in the order they were made. A
// step-up is not one decision with a bigger number on it: it is N decisions,
// each asked, each answered, each paid for, and the record says so -- so the
// log can be read back and the spend adds up whichever step landed.
func persistSteps(steps []decide.RouteResult, u decide.Unit, sink decide.LogSink, usagePath string, reg *decide.Registry, stderr io.Writer) error {
	var failures []string
	for i, step := range steps {
		if err := persist(step, u, sink, usagePath, reg, stderr); err != nil {
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
func persist(res decide.RouteResult, u decide.Unit, sink decide.LogSink, usagePath string, reg *decide.Registry, stderr io.Writer) error {
	if res.Unit == "" {
		return nil // nothing got as far as being a decision
	}
	var failures []string
	if sink != nil {
		if err := sink.Append(decide.EntryFor(res, u, now())); err != nil {
			failures = append(failures, "log: "+err.Error())
		}
	}
	// A decision that made no call writes no usage row: an empty row would be a
	// claim that a call was made.
	if strings.TrimSpace(usagePath) != "" && res.Usage.Calls > 0 {
		if err := appendUsage(usagePath, res, u, reg, stderr); err != nil {
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
func appendUsage(path string, res decide.RouteResult, u decide.Unit, reg *decide.Registry, stderr io.Writer) error {
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
	if usd, note := priceRow(res, reg); usd != "" {
		row["usd"] = usd
	} else if note != "" {
		fmt.Fprintf(stderr, "ROUTE NOTE %s\n", oneline.Escape(note))
	}
	return swarm.AppendCardUsage(path, row)
}

// priceRow is what this call cost in dollars, or the reason the cost is a dash.
// Token spend reporting is an obligation (Glenn), and `usd` was a dash on every
// rc=0 row: the ledger counted tokens and never a cent. The tokens are priced
// from the registry's rate table -- data, per model, with the published rate it
// came from -- and a model the table does not hold is a dash and a NOTE naming
// the model and the row to add. A price nobody published is not invented here.
func priceRow(res decide.RouteResult, reg *decide.Registry) (usd, note string) {
	rate, ok := reg.RateFor(decide.DefaultModel)
	if !ok {
		return "", fmt.Sprintf("no rate for model %s in the registry's rate table, so usd is a dash and the ledger counts tokens but not cost; add {\"model\": %q, \"provider\": %q, \"input_usd_per_mtok\": <n>, \"output_usd_per_mtok\": <n>} to the registry's rates",
			decide.DefaultModel, decide.DefaultModel, usageProvider)
	}
	var missing []string
	if !res.Usage.HasInput {
		missing = append(missing, "input")
	}
	if !res.Usage.HasOutput {
		missing = append(missing, "output")
	}
	if len(missing) > 0 {
		return "", fmt.Sprintf("model %s has a rate but the provider reported no %s tokens for unit %s, so usd is a dash: an unmeasured cost is an absence, never a zero",
			decide.DefaultModel, strings.Join(missing, " or "), oneline.Field(res.Unit))
	}
	return fmt.Sprintf("%.6f", rate.USD(res.Usage.InputTokens, res.Usage.OutputTokens)), ""
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
	// ...and here for the flag path, the one the manager lanes use.
	fromFlags.Kind = decide.CanonicalKind(fromFlags.Kind)
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
	observeVerbFlags("help", fs)
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "help") {
			return 0
		}
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
	logPath := fs.String("log", "", "the escalation log to read: a JSON lines path")
	registry := fs.String("registry", "", "the registry of minds; the embedded ladder when absent")
	summary := fs.Bool("summary", false, "print the per-kind escalation counts and the regenerated starting rung")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	observeVerbFlags("log", fs)
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "log") {
			return 0
		}
		return refuse(stderr, "LOG", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "LOG", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*logPath) == "" {
		return refuse(stderr, "LOG", "bad-arguments", "--log is required; refusing to guess the log's path. Pass the JSON lines log's path; the fleet's decisions are in the fold: nova-pulse fold --db <file> --report")
	}
	if !*summary {
		return refuse(stderr, "LOG", "bad-arguments", "--summary is the read this verb offers; pass it")
	}
	reg, err := decide.LoadRegistry(*registry)
	if err != nil {
		return refuse(stderr, "LOG", "bad-registry", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	// The rows are the record and the summary is a projection of them.
	sink, err := logSinkOpener(*logPath)
	if err != nil {
		return refuse(stderr, "LOG", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	defer sink.Close()
	entries, err := sink.Entries()
	if err != nil {
		return refuse(stderr, "LOG", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	// A voided outcome row is in the log but is no outcome of a decision:
	// drop it before folding so the funnel never counts a row the manager
	// retracted (SPEC-PULSE rule 18, nova-tools #2034).
	entries = excludeVoidedOutcomes(entries)
	sum, err := decide.Summarize(reg, entries)
	if err != nil {
		return refuse(stderr, "LOG", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprint(stdout, sum.Render())
	return 0
}

// excludeVoidedOutcomes drops from a list of entries the one OUTCOME row each
// void record retracts, and drops the void records themselves. The summary
// then never sees a mistake: every stage's count reflects only the rows still
// standing (SPEC-PULSE rule 18, #2034).
//
// A void is keyed by unit AND Time stamp, and each distinct key removes at
// most one outcome row (the earliest match): the stamp has second precision,
// so another unit's outcome in the same second, or a later row of the same
// unit, is never swept up with the one the manager retracted (Stella's hold
// on #2850). Void records are gathered in a pre-pass so a void's position in
// the log relative to its target does not matter.
func excludeVoidedOutcomes(entries []decide.Entry) []decide.Entry {
	type voidKey struct{ unit, time string }
	pending := map[voidKey]bool{}
	for _, e := range entries {
		if strings.TrimSpace(e.Source) != sourceVoid {
			continue
		}
		key := voidKey{unit: outcomeUnit(e), time: voidTargetFromReason(e.Reason)}
		if key.unit != "" && key.time != "" {
			pending[key] = true
		}
	}
	kept := make([]decide.Entry, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.Source) == sourceVoid {
			continue
		}
		if strings.TrimSpace(e.Source) == decide.SourceOutcome {
			key := voidKey{unit: outcomeUnit(e), time: strings.TrimSpace(e.Time)}
			if pending[key] {
				delete(pending, key) // one void key retracts one row
				continue
			}
		}
		kept = append(kept, e)
	}
	return kept
}

// voidTargetFromReason reads the Time stamp a void record's Reason fields,
// returning it for the exclude step. A Reason that does not start with the
// void prefix is not a void we recognise; return empty so the row stands.
func voidTargetFromReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if !strings.HasPrefix(reason, voidPrefix) {
		return ""
	}
	key := strings.TrimSpace(strings.TrimPrefix(reason, voidPrefix))
	if key == "" {
		return ""
	}
	return key
}

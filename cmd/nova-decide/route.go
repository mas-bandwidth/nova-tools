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
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
	fs := flag.NewFlagSet("nova-decide route", flag.ContinueOnError)
	unitPath := fs.String("unit", "", "a JSON file (or inline JSON) holding the unit of work's evidence")
	registry := fs.String("registry", "", "the registry of minds; the embedded ladder when absent")
	logPath := fs.String("log", "", "append the decision to this log (JSON lines)")
	floor := fs.Float64("floor", decide.DefaultFloor, "confidence floor; below it the answer steps UP a rung")
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
	guard := fs.Bool("guard", false, "a guard, the sandbox, sudo, deploy keys or the network is touched")
	secrets := fs.Bool("secrets", false, "secrets are touched")
	freshTake := fs.Bool("fresh-take", false, "this wants a fresh take: a design with one author")
	deadline := fs.String("deadline", "", "the deadline, as a duration such as 45m")
	attempts := &stringList{}
	fs.Var(attempts, "attempt", "a prior attempt as rung:outcome[:reason]; repeatable, in order")
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

	unit, code := buildUnit(*unitPath, set, stderr, decide.Unit{
		ID: *id, Kind: *kind, Files: *files, Packages: *packages, Lanes: *lanes,
		LaneOwner: *laneOwner, Platform: *platform, Guard: *guard, Secrets: *secrets,
		FreshTake: *freshTake, Deadline: *deadline,
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
	if ask {
		client, err := deciderOpener(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, "ROUTE", "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		fmt.Fprintf(stderr, "nova-decide route: asking jev about unit %s (kind %s, floor %.2f)\n", oneline.Field(unit.ID), oneline.Field(unit.Kind), *floor)
		res, err = decide.RouteJev(context.Background(), client, reg, unit, *floor)
		if err != nil {
			return refuse(stderr, "ROUTE", "no-rung", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		fmt.Fprintf(stderr, "nova-decide route: jev answered for unit %s\n", oneline.Field(unit.ID))
	} else {
		res, err = decide.RouteRules(reg, unit, *floor)
		if err != nil {
			return refuse(stderr, "ROUTE", "no-rung", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	if strings.TrimSpace(*logPath) != "" {
		if err := decide.AppendEntry(*logPath, decide.EntryFor(res, unit, now())); err != nil {
			return refuse(stderr, "ROUTE", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	fmt.Fprintln(stdout, res.Line())
	if res.Confidence < *floor {
		return 3
	}
	return 0
}

// buildUnit reads the evidence: a --unit file (or inline JSON), or the unit
// flags, never both. It returns the unit and 0, or a refusal's exit code.
func buildUnit(path string, set map[string]bool, stderr io.Writer, fromFlags decide.Unit, attempts []string) (decide.Unit, int) {
	unitFlags := []string{"unit-id", "kind", "files", "packages", "lanes", "lane-owner", "platform", "guard", "secrets", "fresh-take", "deadline", "attempt"}
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
		if len(parts) == 3 {
			att.Reason = strings.TrimSpace(parts[2])
		}
		fromFlags.Attempts = append(fromFlags.Attempts, att)
	}
	return fromFlags, 0
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

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/redis/go-redis/v9"
)

// preflight is #2756 section 7: one line per check, GREEN or RED with its
// number, exit 1 on any RED, 2 on a usage error with nothing on stdout. The
// store checks are #2947's (rev 3: Redis only, no file flags); the fleet
// checks (#2948, #3004) follow them over the bench registry and beats the
// gatherer reads (#3188). --only store|fleet prints one half. An unreachable
// Redis is not a usage error: every store line is RED, 7.1 unreachable, exit
// 1. --fleet (#3646) is the per-bench readiness review instead: one PASS/FAIL
// row per bench against the declared standard in all.yml, then one fleet
// line, exit 1 on any FAIL.
func init() {
	register(Verb{
		Name:    "preflight",
		Summary: "--redis <host:port> [--sprint <S>] [--only store|fleet]: one GREEN/RED line per check, exit 1 on any RED; --fleet --all-yml <fleet/group_vars/all.yml>: one PASS/FAIL row per bench and a fleet line, exit 1 on any FAIL",
		Run:     cmdPreflight,
	})
}

// fleetLibrary reads the loaded function library for --fleet; a test swaps
// it for a Redis without FUNCTION LIST.
var fleetLibrary = preflight.ReadLibrary

// preflightProcs is 7.26's process snapshot (#3899); a test swaps it, since
// the go test running this package's tests is itself a go test.
var preflightProcs = preflight.ListProcs

func cmdPreflight(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	only := fs.String("only", "", "")
	fleet := fs.Bool("fleet", false, "")
	allYML := fs.String("all-yml", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "preflight", err.Error()+"; it wants --redis <host:port> and optionally --sprint <S> and --only store|fleet, or --fleet --all-yml <fleet/group_vars/all.yml>")
	}
	var problems []string
	if *addr == "" {
		problems = append(problems, "--redis <host:port> is required, the fleet Redis the sprint lives in; "+preflight.SeatHint)
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments")
	}
	switch *only {
	case "", "store", "fleet":
	default:
		problems = append(problems, fmt.Sprintf("--only %q: it is store or fleet", *only))
	}
	var std preflight.Standard
	if *fleet {
		if *allYML == "" {
			problems = append(problems, "--fleet needs --all-yml <fleet/group_vars/all.yml>, the declared nova_build, harness_version and mirrors every row is held to")
		} else if s, err := preflight.ReadStandard(*allYML); err != nil {
			problems = append(problems, err.Error())
		} else {
			std = s
		}
	} else if *allYML != "" {
		problems = append(problems, "--all-yml is read only with --fleet")
	}
	if len(problems) > 0 {
		return refuse(stderr, "preflight", strings.Join(problems, "; "))
	}
	// #3320: one auth path. preflight opens through store.Open like every
	// other verb, as the seat NOVA_SPRINT_REDIS_USER names.
	client, err := preflight.Open(ctx, *addr)
	if err != nil && !(preflight.IsUnreachable(err) && !*fleet) {
		return refuse(stderr, "preflight", err.Error())
	}
	if client != nil {
		defer client.Close()
	}
	if *fleet {
		return preflightFleet(ctx, client, *sprint, std, stdout, stderr)
	}
	code := 0
	if *only != "fleet" {
		var lines []preflight.Line
		if err != nil {
			lines = preflight.Unreachable(err)
		} else {
			lines = preflight.Run(ctx, client, preflight.Options{Sprint: *sprint})
		}
		// #3899: batch tests never run in the coordinator's session. The
		// process snapshot is local, so it is read even when Redis is down.
		procs, perr := preflightProcs(ctx)
		lines = append(lines, preflight.CheckLocalBatchTests(procs, perr, os.Getpid()))
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
		code = preflight.ExitCode(lines)
	}
	if *only == "store" {
		return code
	}
	// #3188: FleetChecks over the gathered registry and beats. A collection
	// this gather does not read prints MISSING and is RED, never GREEN.
	var in preflight.FleetInput
	if err != nil {
		fmt.Fprintf(stderr, "preflight: fleet gather: %v\n", err)
	} else if f, gerr := preflight.GatherFleet(ctx, client, *sprint); gerr != nil {
		fmt.Fprintf(stderr, "preflight: fleet gather: %v\n", gerr)
	} else {
		in = f.Input()
	}
	// #3048: 7.18 reads the friend wake registry the way `friend wake-health` does.
	if err != nil {
		fmt.Fprintf(stderr, "preflight: wake gather: %v\n", err)
	} else if w, werr := gatherWake(ctx, client); werr != nil {
		fmt.Fprintf(stderr, "preflight: wake gather: %v\n", werr)
	} else {
		in.Wake, in.Loaded.Wake = w, true
	}
	fleetLines := preflight.FleetChecks(ctx, in)
	for _, l := range fleetLines {
		fmt.Fprintln(stdout, l)
	}
	if preflight.FleetRed(fleetLines) {
		code = 1
	}
	return code
}

func preflightFleet(ctx context.Context, c *redis.Client, sprint string, std preflight.Standard, stdout, stderr io.Writer) int {
	f, err := preflight.GatherFleet(ctx, c, sprint)
	if err != nil {
		return refuse(stderr, "preflight", "fleet gather: "+err.Error()+"; "+preflight.SeatHint)
	}
	lines, code := preflight.FleetRows(ctx, f, std, fleetLibrary(ctx, c))
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	return code
}

// gatherWake is 7.18's input: the registry read and gate of `friend
// wake-health --all` (life.ReadWake, WakeSnapshot.Rows).
func gatherWake(ctx context.Context, c redis.Cmdable) (preflight.WakeInput, error) {
	snap, err := life.ReadWake(ctx, c)
	if err != nil {
		return preflight.WakeInput{}, err
	}
	in := preflight.WakeInput{DeclPresent: snap.DeclPresent, Friends: len(snap.Friends)}
	for _, r := range snap.Rows() {
		if r.Named {
			in.Named = append(in.Named, preflight.WakeNamed{Friend: r.Friend, Why: r.Why})
		}
	}
	return in, nil
}

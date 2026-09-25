package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/redis/go-redis/v9"
)

// preflight is #2756 section 7: one line per check, GREEN or RED with its
// number, exit 1 on any RED, 2 when it could not run. The store checks are
// #2947's; the fleet checks (#2948, #3004) follow them over the bench
// registry and beats the gatherer reads (#3188). --fleet (#3646) is the
// per-bench readiness review instead: one PASS/FAIL row per bench against the
// declared standard in all.yml, then one fleet line, exit 1 on any FAIL.
func init() {
	register(Verb{
		Name:    "preflight",
		Summary: "--redis <host:port> [--sprint <S>] [--policy-file f] [--unit-env f]... [--launcher-config f] [--retired f]...: one GREEN/RED line per check, exit 1 on any RED; --fleet --all-yml <fleet/group_vars/all.yml>: one PASS/FAIL row per bench and a fleet line, exit 1 on any FAIL",
		Run:     cmdPreflight,
	})
}

// fleetLibrary reads the loaded function library for --fleet; a test swaps
// it for a Redis without FUNCTION LIST.
var fleetLibrary = preflight.ReadLibrary

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func cmdPreflight(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	policy := fs.String("policy-file", "", "")
	launcher := fs.String("launcher-config", "", "")
	fleet := fs.Bool("fleet", false, "")
	allYML := fs.String("all-yml", "", "")
	var unitEnv, retired repeated
	fs.Var(&unitEnv, "unit-env", "")
	fs.Var(&retired, "retired", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "preflight", err.Error()+"; it wants --redis <host:port> and optionally --sprint <S>, or --fleet --all-yml <fleet/group_vars/all.yml>")
	}
	var problems []string
	if *addr == "" {
		problems = append(problems, "--redis <host:port> is required, the fleet Redis the sprint lives in; "+preflight.SeatHint)
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments")
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
	if err != nil {
		return refuse(stderr, "preflight", err.Error())
	}
	defer client.Close()
	if *fleet {
		return preflightFleet(ctx, client, *sprint, std, stdout, stderr)
	}
	lines := preflight.StoreChecks(ctx, client, preflight.Options{
		Sprint: *sprint, PolicyFile: *policy, UnitEnv: unitEnv,
		LauncherConfig: *launcher, Retired: retired,
	})
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	code := preflight.ExitCode(lines)
	// #3188: FleetChecks over the gathered registry and beats. A collection
	// this gather does not read prints MISSING and is RED, never GREEN.
	var in preflight.FleetInput
	if f, err := preflight.GatherFleet(ctx, client, *sprint); err != nil {
		fmt.Fprintf(stderr, "preflight: fleet gather: %v\n", err)
	} else {
		in = f.Input()
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

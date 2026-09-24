package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
)

// preflight is #2756 section 7: one line per check, GREEN or RED with its
// number, exit 1 on any RED, 2 when it could not run. This cut carries the
// store checks (#2947); the fleet checks are #2948.
func init() {
	register(Verb{
		Name:    "preflight",
		Summary: "--redis <host:port> [--sprint <S>] [--policy-file f] [--unit-env f]... [--launcher-config f] [--retired f]...: one GREEN/RED line per check, exit 1 on any RED",
		Run:     cmdPreflight,
	})
}

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
	var unitEnv, retired repeated
	fs.Var(&unitEnv, "unit-env", "")
	fs.Var(&retired, "retired", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "preflight", err.Error()+"; it wants --redis <host:port> and optionally --sprint <S>")
	}
	var problems []string
	if *addr == "" {
		problems = append(problems, "--redis <host:port> is required, the fleet Redis the sprint lives in; "+preflight.SeatHint)
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments")
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
	lines := preflight.StoreChecks(ctx, client, preflight.Options{
		Sprint: *sprint, PolicyFile: *policy, UnitEnv: unitEnv,
		LauncherConfig: *launcher, Retired: retired,
	})
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	return preflight.ExitCode(lines)
}

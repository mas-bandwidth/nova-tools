// The fleet-state and fleet-live verbs (recut of nova-tools PR #2752) register
// themselves through the S0 registry (registry.go). They are the production
// callers of internal/fleet/state: both read the fleet store through
// state.ReadRedis and decide every bench by its heartbeat key and that key's
// TTL -- never by load, never by a probe (#2161).
//
//	nova-sprint fleet-state --redis <addr>   one `name<TAB>UP|HELD|DOWN` row per registered bench
//	nova-sprint fleet-live  --redis <addr>   one name per bench whose key still counts (UP or HELD)
//
// Both are read-only and print to stdout; nothing reads their output back as
// authority -- the keys are the present.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/fleet/state"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "fleet-state",
		Summary: "one name<TAB>UP|HELD|DOWN row per registered bench, by its heartbeat key's TTL",
		Run:     runFleetVerb("fleet-state"),
	})
	register(Verb{
		Name:    "fleet-live",
		Summary: "the benches whose heartbeat key still counts (UP or HELD), one per line",
		Run:     runFleetVerb("fleet-live"),
	})
}

func runFleetVerb(verb string) func(context.Context, []string, io.Writer, io.Writer) int {
	return func(ctx context.Context, args []string, out, errOut io.Writer) int {
		fs := flag.NewFlagSet(verb, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Usage = func() {}
		redisAddr := fs.String("redis", "", "")
		if err := fs.Parse(args); err != nil {
			return refuse(errOut, verb, err.Error())
		}
		if fs.NArg() != 0 {
			return refuse(errOut, verb, "takes no arguments after the flags")
		}
		if *redisAddr == "" {
			return refuse(errOut, verb, "--redis addr is required")
		}
		st, err := store.Open(ctx, *redisAddr)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		defer st.Close()
		benches, now, err := state.ReadRedis(ctx, st.Client())
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		if verb == "fleet-live" {
			for _, name := range state.Live(benches, now) {
				fmt.Fprintln(out, name)
			}
			return 0
		}
		for _, b := range benches {
			fmt.Fprintln(out, state.Row(b, now))
		}
		return 0
	}
}

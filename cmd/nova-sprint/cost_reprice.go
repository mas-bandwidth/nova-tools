package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// cmdCostReprice is `cost reprice`: every priced consumer record of the sprint computed
// again from its stored tokens at its route row's current prices (docs/SPEC-SPRINT.md,
// "What a card cost", the reprice; the owner, 2026-10-05: "Is it possible to fix
// historical prices for this sprint ... More accurate prices allow us to optimize
// better."), in one step (sprint.RepriceOf): the records, each card's totals, each landed
// card's cost and its stream's sum. It prints per route the old sum, the new sum and the
// records, and the records it left; --dry-run plans it on a read of the tables and writes
// nothing.
func (a *app) cmdCostReprice(args []string, stdout, stderr io.Writer) int {
	const verb = "cost reprice"
	fs, c := a.verbSetup(verb)
	var routes listFlag
	fs.Var(&routes, "route", "reprice only the records this route priced (again, or comma separated, for more; default: every route)")
	since := fs.String("since", "", "reprice only the records that ended at or after this time (RFC3339)")
	dry := fs.Bool("dry-run", false, "print what the reprice would do, and write nothing")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			return refuse(stderr, verb, fmt.Sprintf("--since %q is not a time: give it as RFC3339, 2026-10-01T00:00:00Z", *since))
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	ctx := context.Background()
	req := sprint.RepriceReq{Routes: routes, Since: *since, Who: c.actor}
	var rep sprint.RepriceReport
	if *dry {
		// the step's own plan on a read of the tables, applied nowhere
		rs, _, err := st.Routes(ctx)
		if err != nil {
			return a.readFailed(verb, err, stderr)
		}
		s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
		if err != nil {
			return a.readFailed(verb, err, stderr)
		}
		s.Routes = rs
		var p sprint.Plan
		p, rep = sprint.RepriceOf(s, req)
		if len(p.Refused) > 0 {
			return refuse(stderr, verb, p.Refused[0].Why)
		}
	} else {
		// the account is the plan's the step committed: its last attempt's
		step := store.Step{Named: true, Args: store.ArgsOf(req), Verb: verb, Load: []string{sprint.Work, sprint.Merge}, Routes: true, Mirrors: true,
			Plan: func(s *sprint.Snapshot) sprint.Plan {
				var p sprint.Plan
				p, rep = sprint.RepriceOf(s, req)
				return p
			}}
		res, err := st.Run(ctx, step)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		if len(res.Refused) > 0 {
			return refuse(stderr, verb, res.Refused[0].Why)
		}
		if res.Lost {
			return refuse(stderr, verb, "the reprice lost every attempt to other writers and wrote nothing: run it again")
		}
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"routes": orEmptyRoutes(rep.Routes), "no_tokens": rep.NoTokens, "subscription": rep.Subscription,
			"no_route": rep.NoRoute, "route_gone": rep.RouteGone, "cut": rep.Cut, "cards": rep.Cards, "streams": rep.Streams, "dry_run": *dry})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, rr := range rep.Routes {
		held := ""
		if rr.HeldByActual > 0 {
			held = fmt.Sprintf(" held_by_actual=%d", rr.HeldByActual)
		}
		fmt.Fprintf(stdout, "REPRICE route=%s price_as_of=%s records=%d changed=%d old=%s new=%s old_usd=%s new_usd=%s old_predicted_usd=%s new_predicted_usd=%s%s\n",
			oneline.Field(rr.Route), oneline.Field(orDashStr(rr.AsOf, "-")), rr.Records, rr.Changed, sprint.MoneyText(rr.OldCharged), sprint.MoneyText(rr.NewCharged),
			rr.OldCharged, rr.NewCharged, rr.OldPredicted, rr.NewPredicted, held)
	}
	left := fmt.Sprintf("no_tokens=%d subscription=%d no_route=%d cut=%d", rep.NoTokens, rep.Subscription, rep.NoRoute, rep.Cut)
	for _, r := range slices.Sorted(maps.Keys(rep.RouteGone)) {
		left += fmt.Sprintf(" route_gone=%s:%d", oneline.Field(r), rep.RouteGone[r])
	}
	fmt.Fprintf(stdout, "REPRICE LEFT %s\n", left)
	if *dry {
		fmt.Fprintf(stdout, "COST REPRICE DRY-RUN routes=%d cards=%d streams=%d: nothing was written\n", len(rep.Routes), rep.Cards, rep.Streams)
		return 0
	}
	fmt.Fprintf(stdout, "COST REPRICE OK routes=%d cards=%d streams=%d\n", len(rep.Routes), rep.Cards, rep.Streams)
	return 0
}

// orEmptyRoutes is the routes as --json shows them: [] for none, never null.
func orEmptyRoutes(rs []sprint.RepriceRoute) []sprint.RepriceRoute {
	if rs == nil {
		return []sprint.RepriceRoute{}
	}
	return rs
}

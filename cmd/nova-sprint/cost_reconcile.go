package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/provbalance"
)

// cmdCostReconcile is `cost reconcile`: the cost reconciliation run once (docs/SPEC-SPRINT.md,
// "What a card cost", the reconciliation; the owner, 2026-10-04: "we MUST track the complete
// cost"). Each provider the routes name is asked its own count of today's usage (the UTC day)
// through the seat's key in this environment (pkg/provbalance.ReadUsage), the reads are
// set beside the sprint's records of the same day in one step (sprint.CostReconcile: each
// provider's record on the fleet table, and its one gap judgment opened past the bound or
// closed within it), and each provider's gap is printed. The release's spend check calls it.
func (a *app) cmdCostReconcile(args []string, stdout, stderr io.Writer) int {
	const verb = "cost reconcile"
	fs, c := a.verbSetup(verb)
	dry := fs.Bool("dry-run", false, "read each provider's usage and print each gap as the step would record it, and write nothing")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	ctx := context.Background()
	routes, _, err := st.Routes(ctx)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	names := map[string]bool{}
	for _, r := range routes {
		if r.Provider != "" {
			names[r.Provider] = true
		}
	}
	day := a.now().UTC().Format("2006-01-02")
	var reads []sprint.UsageRead
	for _, p := range slices.Sorted(maps.Keys(names)) {
		reads = append(reads, provbalance.ReadUsage(ctx, a.transport, p, day, a.getenv))
	}
	if len(reads) == 0 {
		// nothing to read and nothing written: no provider is named by a route
		if c.json {
			fmt.Fprintln(stdout, `{"providers":[],"notes":0}`)
			return 0
		}
		fmt.Fprintln(stdout, "COST RECONCILE OK providers=0 notes=0: the routes name no provider (nova-sprint routes)")
		return 0
	}
	req := sprint.CostReconcileReq{Reads: reads, Who: c.actor}
	notes, word := 0, "OK"
	fleet := sprint.NewTable(sprint.Fleet)
	if *dry {
		// the step's own plan on a read of the tables, applied nowhere
		s, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet}, nil)
		if err != nil {
			return a.readFailed(verb, err, stderr)
		}
		s.Routes = routes
		plan := sprint.CostReconcile(s, req)
		for _, pw := range plan.Props {
			fleet.SetProp(pw.Name, pw.Value)
		}
		notes, word = len(plan.Notes), "DRY-RUN"
	} else {
		res, err := st.Run(ctx, store.CostReconcileStep(req))
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		if len(res.Refused) > 0 {
			return refuse(stderr, verb, res.Refused[0].Why)
		}
		s, err := st.Load(ctx, []string{sprint.Fleet}, nil)
		if err != nil {
			return a.readFailed(verb, err, stderr)
		}
		fleet, notes = s.Fleet, res.Notes
	}
	var recs []sprint.CostReconcileRecord
	for _, rd := range reads {
		if rec, ok := sprint.CostReconcileOf(fleet, rd.Provider); ok {
			recs = append(recs, rec)
		}
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"providers": recs, "notes": notes, "dry_run": *dry})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, rec := range recs {
		if !rec.Known {
			fmt.Fprintf(stdout, "COST provider=%s unknown: %s\n", oneline.Field(rec.Provider), oneline.Escape(rec.Note))
			continue
		}
		fmt.Fprintf(stdout, "COST provider=%s day=%s provider_usd=%s records=%s gap=%s share=%.1f%%\n",
			oneline.Field(rec.Provider), rec.Day, sprint.Dollars(rec.Used), sprint.Dollars(rec.Internal), sprint.Dollars(rec.Gap), rec.Share*100)
	}
	if *dry {
		fmt.Fprintf(stdout, "COST RECONCILE %s providers=%d notes=%d: nothing was written\n", word, len(recs), notes)
		return 0
	}
	fmt.Fprintf(stdout, "COST RECONCILE %s providers=%d notes=%d\n", word, len(recs), notes)
	return 0
}

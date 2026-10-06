package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbClasses["stats recount"] = classMachine
}

func (a *app) cmdStatsRecount(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("stats recount")
	dryRun := fs.Bool("dry-run", false, "print before/after counters per row without writing them back")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "stats recount", argErr("takes no positional arguments ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "stats recount", err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Fleet, sprint.Readers}, sprint.StatsRecords)
	if err != nil {
		return a.readFailed("stats recount", err, stderr)
	}
	plan, rows := sprint.RecountPlan(s)
	if *dryRun {
		fmt.Fprintln(stdout, "STATS RECOUNT DRY-RUN")
		for _, r := range rows {
			fmt.Fprintf(stdout, "row %-20s ok: %d -> %d | failed: %d -> %d | withdrawn: %d -> %d\n",
				r.Row, r.BeforeOK, r.AfterOK, r.BeforeFail, r.AfterFail, r.BeforeWith, r.AfterWith)
		}
		return 0
	}
	if !plan.Empty() {
		_, err := st.Run(context.Background(), store.Step{
			Verb: "stats recount",
			Load: []string{sprint.Fleet, sprint.Work},
			Plan: func(snap *sprint.Snapshot) sprint.Plan {
				p, _ := sprint.RecountPlan(snap)
				return sprint.Lawful(p)
			},
		})
		if err != nil {
			fmt.Fprintf(stderr, "%s stats recount: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "row %-20s ok: %d -> %d | failed: %d -> %d | withdrawn: %d -> %d\n",
			r.Row, r.BeforeOK, r.AfterOK, r.BeforeFail, r.AfterFail, r.BeforeWith, r.AfterWith)
	}
	fmt.Fprintf(stdout, "STATS RECOUNT OK epoch=%d rows=%d\n", s.Epoch, len(rows))
	return 0
}

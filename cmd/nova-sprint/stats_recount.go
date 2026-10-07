package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	verbClasses["stats recount"] = classCoordinator
}

// cmdStatsRecount re-derives friend, member, route and stream counters from the
// attempt records (sprint.RecountPlan). --dry-run prints each row and writes nothing.
func (a *app) cmdStatsRecount(args []string, stdout, stderr io.Writer) int {
	const verb = "stats recount"
	fs, c := a.verbSetup(verb)
	dry := fs.Bool("dry-run", false, "print each row before and after; write nothing")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	rows, epoch, err := st.Recount(context.Background(), *dry)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	if c.json {
		b, _ := json.Marshal(struct {
			Epoch uint64              `json:"epoch"`
			Dry   bool                `json:"dry_run,omitempty"`
			Rows  []sprint.RowRecount `json:"rows"`
		}{epoch, *dry, rows})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if rows == nil {
		rows = []sprint.RowRecount{}
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "row %s ok: %d -> %d | failed: %d -> %d | withdrawn: %d -> %d\n",
			r.Row, r.BeforeOK, r.AfterOK, r.BeforeFail, r.AfterFail, r.BeforeWith, r.AfterWith)
	}
	fmt.Fprintf(stdout, "STATS RECOUNT OK epoch=%d rows=%d\n", epoch, len(rows))
	return 0
}

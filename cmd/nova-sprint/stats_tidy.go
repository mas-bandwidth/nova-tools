package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

func init() {
	// the coordinator's, as stream archive is (the class test, coordinator.go)
	verbClasses["stats tidy"] = classCoordinator
}

// cmdStatsTidy starts the statistics afresh and keeps the work (store.TidyStats; the
// owner, 2026-10-06: "can you please clear the sets of done consumer cards for all friends
// and fleet", "I would like a semi-fresh start to stats now"): the kinds named (--all is
// the four) move to a dated archive record and count from now. Refused, exit 1 and nothing
// written, within a minute of the last tidy; --dry-run writes nothing and prints what
// would move.
func (a *app) cmdStatsTidy(args []string, stdout, stderr io.Writer) int {
	const verb = "stats tidy"
	fs, c := a.verbSetup(verb)
	friends := fs.Bool("friends", false, "the friends' done cells: their history-only finished cards leave them (done and ok% count from now)")
	fleet := fs.Bool("fleet", false, "the machines' done cells, as --friends does the friends'")
	routes := fs.Bool("routes", false, "the route counters: archived, and stats --routes counts from now")
	streams := fs.Bool("streams", false, "the streams' landed costs: the work table's cost and per landed count from now")
	all := fs.Bool("all", false, "all four: --friends --fleet --routes --streams")
	reason := fs.String("reason", "", "why, kept in the archive and the stats record (required)")
	dry := fs.Bool("dry-run", false, "print what would move; write nothing")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	var kinds []string
	for i, on := range []bool{*friends, *fleet, *routes, *streams} {
		if on || *all {
			kinds = append(kinds, sprint.TidyKinds[i])
		}
	}
	if len(kinds) == 0 {
		return refuse(stderr, verb, "names nothing to tidy: give --friends, --fleet, --routes, --streams or --all")
	}
	if strings.TrimSpace(*reason) == "" {
		return refuse(stderr, verb, "wants --reason <text>: the archive keeps why")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	res, err := st.TidyStats(context.Background(), store.TidyReq{Kinds: kinds, Reason: *reason, DryRun: *dry})
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	if res.Refused != "" {
		fmt.Fprintf(stderr, "%s %s REFUSED: %s; run: nova-sprint stats\n", prog, verb, oneline.Escape(res.Refused))
		return 1
	}
	if !c.json {
		for _, l := range res.Said {
			fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
		}
		for _, r := range res.Record.Rows {
			fmt.Fprintf(stdout, "ROW %s ok=%d failed=%d off=%d kept=%d\n", r.Row, r.OK, r.Failed, len(r.Moved), len(r.Kept))
		}
		for _, name := range slices.Sorted(maps.Keys(res.Record.Streams)) {
			b := res.Record.Streams[name]
			fmt.Fprintf(stdout, "STREAM %s cost=%s landed=%d\n", name, sprint.MoneyText(b.Cost), b.Landed)
		}
		if len(res.Record.Routes) > 0 {
			fmt.Fprintf(stdout, "ROUTES %d archived\n", len(res.Record.Routes))
		}
	}
	since := res.Record.At.Format(time.RFC3339)
	facts := map[string]any{"since": since, "kinds": kinds, "archive": res.Archive, "moved": res.Moved, "kept": res.Kept,
		"rows": res.Record.Rows, "streams": res.Record.Streams, "routes": len(res.Record.Routes), "dry_run": res.DryRun, "said": res.Said}
	word := "STATS-TIDY OK"
	if res.DryRun {
		word = "STATS-TIDY DRY-RUN would move"
	}
	sayOK(stdout, c.json, verb, fmt.Sprintf("%s since=%s kinds=%s moved=%d kept=%d archive=%s", word, since, strings.Join(kinds, ","), res.Moved, res.Kept, res.Archive), facts)
	return 0
}

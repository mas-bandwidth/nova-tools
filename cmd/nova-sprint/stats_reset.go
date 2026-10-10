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
	// the coordinator's, as stats tidy is (the class test, coordinator.go)
	verbClasses["stats reset"] = classCoordinator
}

// cmdStatsReset marks the counters as they stand, and every figure counts from the mark
// (store.ResetStats; the owner, 2026-10-09: "Clear the cost per-card right now. Clear the
// per-tier costs. Clear the total cost.", "Clear the done and the ok% for all friends now.",
// "everything that I described should happen when you go reset. tidy is different."): each
// fleet and friend row's done and ok%, each stream's cost and spend by tier, the sprint's
// total cost and per card, and stats. Nothing moves: no card, no stream, no archive. A
// second reset replaces the mark; --dry-run writes nothing and prints the mark it would
// write; --show prints the mark in force; --op <id> again returns the mark it wrote.
func (a *app) cmdStatsReset(args []string, stdout, stderr io.Writer) int {
	const verb = "stats reset"
	fs, c := a.verbSetup(verb)
	reason := fs.String("reason", "", "why, kept in the mark (required, but with --show)")
	dry := fs.Bool("dry-run", false, "print the mark it would write; write nothing")
	show := fs.Bool("show", false, "print the mark in force; write nothing")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	if *show && (*dry || *reason != "") {
		return refuse(stderr, verb, "--show only reads the mark: give it alone")
	}
	if !*show && strings.TrimSpace(*reason) == "" {
		return refuse(stderr, verb, "wants --reason <text>: the mark keeps why")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	ctx := context.Background()
	if *show {
		m, err := st.StatsReset(ctx)
		if err != nil {
			return a.readFailed(verb, err, stderr)
		}
		if m == nil {
			sayOK(stdout, c.json, verb, "STATS-RESET none: every figure counts from the epoch's start or the last tidy", map[string]any{"mark": nil})
			return 0
		}
		if !c.json {
			printMark(stdout, *m)
		}
		sayOK(stdout, c.json, verb, "STATS-RESET mark "+markLine(*m), map[string]any{"mark": m})
		return 0
	}
	res, err := st.ResetStats(ctx, store.ResetReq{Reason: *reason, DryRun: *dry, Op: c.op})
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	if res.Refused != "" {
		fmt.Fprintf(stderr, "%s %s REFUSED: %s; run: nova-sprint stats reset --show\n", prog, verb, oneline.Escape(res.Refused))
		return 1
	}
	if !c.json {
		for _, l := range res.Said {
			fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
		}
		printMark(stdout, res.Mark)
	}
	word := "STATS-RESET OK"
	switch {
	case res.DryRun:
		word = "STATS-RESET DRY-RUN would mark"
	case res.Replay:
		word = "STATS-RESET REPLAY op=" + res.Mark.Op + " nothing written; mark"
	}
	line := word + " " + markLine(res.Mark)
	if res.Replaced != nil {
		line += " replaced=" + res.Replaced.At.Format(time.RFC3339Nano)
	}
	sayOK(stdout, c.json, verb, line, map[string]any{"mark": res.Mark, "replaced": res.Replaced, "dry_run": res.DryRun, "replay": res.Replay, "said": res.Said})
	return 0
}

// markLine is a mark in one line: when, by whom, its rows and streams and the epoch's total.
func markLine(m sprint.ResetMark) string {
	return fmt.Sprintf("at=%s by=%s rows=%d streams=%d total=%s reason=%s", m.At.Format(time.RFC3339Nano), cmpDash(m.By),
		len(m.Rows), len(m.Streams), sprint.MoneyText(strings.TrimPrefix(m.TotalCost, "$")), oneline.Escape(m.Reason))
}

// printMark is the mark's counters, a line a row and a stream, in name order.
func printMark(w io.Writer, m sprint.ResetMark) {
	for _, row := range slices.Sorted(maps.Keys(m.Rows)) {
		r := m.Rows[row]
		fmt.Fprintf(w, "ROW %s done=%d ok=%d failed=%d\n", row, r.Done(), r.OK, r.Failed)
	}
	for _, name := range slices.Sorted(maps.Keys(m.Streams)) {
		s := m.Streams[name]
		var tiers []string
		for _, t := range slices.Sorted(maps.Keys(s.CostByTier)) {
			tiers = append(tiers, t+"="+s.CostByTier[t])
		}
		fmt.Fprintf(w, "STREAM %s landed=%d cost=%s total=%s tiers=%s\n", name, s.Landed, sprint.MoneyText(s.Cost),
			cmpDash(s.TotalCost), cmpDash(strings.Join(tiers, ",")))
	}
}

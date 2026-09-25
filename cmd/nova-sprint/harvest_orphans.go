// `nova-sprint card harvest --orphans` (#3796): one harvest.RunOrphans pass
// (#3042, spec #2756 3.2 row "orphan-effect") over one sprint's benches, and
// one receipt line. An orphan ends only on the end record of its own identity
// under <results>/<identity>; a later DONE attempt supersedes it (PR closed,
// branch renamed orphan/<S>/<label>-a<n>) under the bench's harvest lease.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

type orphanFlags struct {
	redis, sprint string
	benches       listFlag
	labels        []string
	loop          bool
	results       string
	grace, clock  time.Duration
	instance      string
}

// runHarvestOrphans exits 0 when every bench swept and nothing failed; 1 an
// orphan or a bench did not finish (the receipt counts them); 2 usage or
// Redis; 3 a bench lease is held elsewhere or was lost. Usage is refused
// before the store is opened.
func runHarvestOrphans(ctx context.Context, f orphanFlags, out, errOut io.Writer) int {
	const verb = "card harvest"
	if f.sprint == "" || f.sprint == "all" {
		return refuse(errOut, verb, "--orphans wants one --sprint <S>")
	}
	if f.loop {
		return refuse(errOut, verb, "--orphans is one pass; drop --loop")
	}
	if len(f.labels) > 0 {
		return refuse(errOut, verb, "--orphans takes no labels; it sweeps the sprint's orphan-effect cards per bench")
	}
	if f.results == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			f.results = filepath.Join(home, "nova-bench", "results")
		}
	}
	if !filepath.IsAbs(f.results) {
		return refuse(errOut, verb, "--results wants an absolute dir (the results root the end records land under)")
	}
	if f.grace < 0 {
		return refuse(errOut, verb, "--grace must not be negative")
	}
	st, err := store.Open(ctx, f.redis)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	if f.instance == "" {
		host, _ := os.Hostname()
		f.instance = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	plan, err := harvest.NewPlan(ctx, st, f.sprint, f.benches)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	start := time.Now()
	results := harvest.RunOrphans(ctx, st, harvest.OrphanOptions{
		Sprint: f.sprint, Benches: plan.Benches, ResultsRoot: f.results, Grace: f.grace,
		Clock: f.clock, Instance: f.instance, Forge: harvest.GitHub{},
	})
	return printOrphans(out, f.sprint, len(plan.NoBeat), results, time.Since(start))
}

// printOrphans is the one receipt line of an --orphans pass.
func printOrphans(out io.Writer, sprint string, noBeat int, results []harvest.OrphanResult, took time.Duration) int {
	var ended, superseded, waiting, recut, failed, unfinished int
	code, benchErr, cardErr := 0, "", ""
	for _, r := range results {
		ended += len(r.Ended)
		superseded += len(r.Superseded)
		waiting += len(r.Waiting)
		recut += len(r.RecutDue)
		failed += len(r.Failed)
		if len(r.Failed) > 0 && cardErr == "" {
			cardErr = r.Failed[0].Label + ": " + r.Failed[0].Err.Error()
		}
		if r.Err == nil {
			continue
		}
		unfinished++
		if benchErr == "" {
			benchErr = r.Bench + ": " + r.Err.Error()
		}
		if errors.Is(r.Err, harvest.ErrLeaseHeld) || errors.Is(r.Err, harvest.ErrFenced) {
			code = 3
		} else if code == 0 {
			code = 1
		}
	}
	status := "OK"
	switch {
	case code == 3:
		status = "LEASE"
	case unfinished > 0:
		status = "UNFINISHED"
	case failed > 0:
		status, code = "PARTIAL", 1
	}
	line := fmt.Sprintf("HARVEST-ORPHANS %s sprint=%s benches=%d no_beat=%d ended=%d superseded=%d waiting=%d recut_due=%d failed=%d took_ms=%d",
		status, sprint, len(results), noBeat, ended, superseded, waiting, recut, failed, took/time.Millisecond)
	if benchErr == "" {
		benchErr = cardErr
	}
	if benchErr != "" {
		line += " err=" + oneline.Escape(benchErr)
	}
	_, _ = fmt.Fprintln(out, line)
	return code
}

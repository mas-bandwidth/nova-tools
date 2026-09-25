// `nova-sprint card harvest` (#2932, spec #2756 4.3): one worker per bench,
// every bench in parallel, each under its own clock; push the card's branch
// from its bench, find or open the PR under pr:<repo>:<branch>, write the PR
// record pr:<repo>:<n> and read the head back from it, then harvested. The
// reconciler runs the same pass as its harvest duty (consume.go).
//
// The card verb may already be registered by card_run.go (#2928); this file
// then wraps it and adds the harvest subverb, so neither file edits the other.
// card_run.go sorts first, so its init runs first.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	if prev, ok := verbs["card"]; ok {
		verbs["card"] = Verb{Name: "card", Summary: prev.Summary + "; harvest per bench",
			Run: func(ctx context.Context, args []string, out, errOut io.Writer) int {
				if len(args) > 0 && args[0] == "harvest" {
					return runCardHarvest(ctx, args[1:], out, errOut)
				}
				return prev.Run(ctx, args, out, errOut)
			}}
		return
	}
	register(Verb{Name: "card", Summary: "harvest ended cards per bench: push, find-or-open the PR, verify the head",
		Run: func(ctx context.Context, args []string, out, errOut io.Writer) int {
			if len(args) > 0 && args[0] == "harvest" {
				return runCardHarvest(ctx, args[1:], out, errOut)
			}
			return refuse(errOut, "card", "wants harvest")
		}})
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

// Exit 0 every bench finished its pass with every card harvested; 1 a card or
// a bench did not finish (the others still harvested; the lines say which);
// 2 usage or Redis; 3 a bench lease is held elsewhere or was lost.
//
//	nova-sprint card harvest [--redis <addr>] [--sprint <S>|all] [--bench <b>[,<b>]|all]
//	    (--once | --loop [--every 10s]) [--clock 5m] [<label>...]
//
// --redis defaults to $NOVA_SPRINT_REDIS; the owner comes from each card's
// full repo field (--owner is gone); GitHub is the caller's own GH_CONFIG_DIR
// over REST.
func runCardHarvest(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("card harvest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, a := range args {
		if a == "--results-root" || strings.HasPrefix(a, "--results-root=") || a == "-results-root" || strings.HasPrefix(a, "-results-root=") {
			// #3329: deleted, not ignored; a launcher still passing it is told where the dir lives now.
			return refuse(errOut, "card harvest", "unknown flag --results-root: the results dir is the absolute card hash field s:<S>:card:<label> results, written by card end")
		}
	}
	redisAddr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	sprint := fs.String("sprint", "all", "")
	var benches listFlag
	fs.Var(&benches, "bench", "")
	clock := fs.Duration("clock", harvest.DefaultClock, "")
	instance := fs.String("instance", "", "")
	once := fs.Bool("once", false, "")
	loop := fs.Bool("loop", false, "")
	every := fs.Duration("every", 10*time.Second, "")
	orphans := fs.Bool("orphans", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "card harvest", err.Error())
	}
	labels := fs.Args()
	if *orphans {
		return refuse(errOut, "card harvest", "--orphans is the C3 build (orphan-effect, #2756 3.2), not this one")
	}
	if *once == *loop {
		// Neither or both: the mode is usage, refused before the store is opened,
		// so a bare `card harvest` never runs a mutating pass.
		return refuse(errOut, "card harvest", "wants exactly one of --once or --loop: (--once | --loop [--every 10s])")
	}
	if *loop && *every <= 0 {
		return refuse(errOut, "card harvest", "--every must be positive")
	}
	if len(labels) > 0 && (*sprint == "" || *sprint == "all") {
		return refuse(errOut, "card harvest", "labels want one --sprint <S>")
	}
	if len(labels) > 0 && len(benches) > 0 {
		return refuse(errOut, "card harvest", "wants --bench or labels, not both")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "card harvest", err.Error())
	}
	defer st.Close()
	if *instance == "" {
		host, _ := os.Hostname()
		*instance = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	for {
		code := cardHarvestPass(ctx, st, *sprint, benches, labels, *clock, *instance, out, errOut)
		if !*loop || code == 2 {
			return code
		}
		select {
		case <-ctx.Done():
			return code
		case <-time.After(*every):
		}
	}
}

func cardHarvestPass(ctx context.Context, st *store.Store, sprint string, benches listFlag, labels []string,
	clock time.Duration, instance string, out, errOut io.Writer) int {
	var only map[string]bool
	if len(labels) > 0 {
		only = map[string]bool{}
		reads := make([]store.HashRead, len(labels))
		for i, l := range labels {
			reads[i] = store.HashRead{Key: "s:" + sprint + ":card:" + l, Fields: []string{"bench"}}
			only[l] = true
		}
		vals, err := st.PipelineHMGet(ctx, reads)
		if err != nil {
			return refuse(errOut, "card harvest", err.Error())
		}
		seen := map[string]bool{}
		benches = nil
		for i, v := range vals {
			b, _ := v[0].(string)
			if b == "" {
				return refuse(errOut, "card harvest", "card "+labels[i]+" has no bench in sprint "+sprint)
			}
			if !seen[b] {
				seen[b] = true
				benches = append(benches, b)
			}
		}
	}
	plan, err := harvest.NewPlan(ctx, st, sprint, benches)
	if err != nil {
		return refuse(errOut, "card harvest", err.Error())
	}
	for _, b := range plan.NoBeat {
		_, _ = fmt.Fprintf(out, "HARVEST SKIP bench=%s reason=no-beat\n", b)
	}
	if len(benches) > 0 && len(plan.Benches) > 0 {
		reads := make([]store.HashRead, len(plan.Benches))
		for i, b := range plan.Benches {
			reads[i] = store.HashRead{Key: "bench:" + b + ":state", Fields: []string{"state"}}
		}
		vals, err := st.PipelineHMGet(ctx, reads)
		if err != nil {
			return refuse(errOut, "card harvest", err.Error())
		}
		var up []string
		for i, b := range plan.Benches {
			state, _ := vals[i][0].(string)
			if state == "" {
				state = "DOWN"
			}
			if state != "UP" {
				_, _ = fmt.Fprintf(out, "bench %s skipped: %s\n", b, strings.ToLower(state))
				continue
			}
			up = append(up, b)
		}
		plan.Benches = up
	}
	code := 0
	for _, s := range plan.Sprints {
		results := harvest.Run(ctx, st, harvest.Options{
			Sprint: s, Benches: plan.Benches, Labels: only, Clock: clock, Instance: instance,
			Forge:  harvest.GitHub{},
			Pusher: harvest.SSHPusher{},
		})
		if c := printHarvest(out, s, results); c > code {
			code = c
		}
	}
	return code
}

func printHarvest(out io.Writer, sprint string, results []harvest.BenchResult) int {
	code := 0
	for _, r := range results {
		for _, c := range r.Cards {
			_, _ = fmt.Fprintf(out, "HARVESTED %s %s pr=%d head=%s via=%s bench=%s\n",
				sprint, c.Label, c.PR, c.Head, c.Via, r.Bench)
		}
		for _, f := range r.Failed {
			_, _ = fmt.Fprintf(out, "HARVEST-FAILED %s %s bench=%s err=%s detail=%s%s\n", sprint, f.Label, r.Bench, f.Code, oneline.Escape(f.Err.Error()), harvestFails(f))
			if code == 0 {
				code = 1
			}
		}
		status := "OK"
		switch {
		case r.Err != nil:
			status = "UNFINISHED"
			if code != 3 {
				code = 1
			}
			if strings.Contains(r.Err.Error(), harvest.ErrLeaseHeld.Error()) || strings.Contains(r.Err.Error(), harvest.ErrFenced.Error()) {
				status, code = "LEASE", 3
			}
		case len(r.Failed) > 0:
			status = "PARTIAL"
		}
		line := fmt.Sprintf("HARVEST %s bench=%s n=%d failed=%d took_ms=%d", status, r.Bench, len(r.Cards), len(r.Failed), r.Took/time.Millisecond)
		if r.Err != nil {
			line += " err=" + oneline.Escape(r.Err.Error())
		}
		_, _ = fmt.Fprintln(out, line)
	}
	return code
}

// harvestFails is the HARVEST-FAILED line's tail (#3712): the card's failed
// passes so far, and card=done/fail on the pass that reached the cap.
func harvestFails(f harvest.CardFailure) string {
	if f.Fails == 0 {
		return ""
	}
	tail := fmt.Sprintf(" fails=%d", f.Fails)
	if f.Moved {
		tail += " card=done/fail"
	}
	return tail
}

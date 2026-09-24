// `nova-sprint card harvest` (#2932, spec #2756 4.3): one worker per bench,
// every bench in parallel, each under its own clock; push the card's branch
// from its bench, find or open the PR under pr:<repo>:<branch>, read the head
// back by REST, then harvested.
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
func runCardHarvest(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("card harvest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, a := range args {
		if a == "--results-root" || strings.HasPrefix(a, "--results-root=") || a == "-results-root" || strings.HasPrefix(a, "-results-root=") {
			// #3329: deleted, not ignored; a launcher still passing it is told where the dir lives now.
			return refuse(errOut, "card harvest", "unknown flag --results-root: the results dir is the absolute card hash field s:<S>:card:<label> results, written by card end")
		}
	}
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	var benches listFlag
	fs.Var(&benches, "bench", "")
	clock := fs.Duration("clock", harvest.DefaultClock, "")
	instance := fs.String("instance", "", "")
	owner := fs.String("owner", "mas-bandwidth", "")
	orphans := fs.Bool("orphans", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "card harvest", err.Error())
	}
	labels := fs.Args()
	if *orphans {
		return refuse(errOut, "card harvest", "--orphans is the C3 build (orphan-effect, #2756 3.2), not this one")
	}
	if *sprint == "" || (len(benches) == 0) == (len(labels) == 0) {
		return refuse(errOut, "card harvest", "wants --sprint and either --bench <b>[,<b>...] or labels")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "card harvest", err.Error())
	}
	defer st.Close()

	var only map[string]bool
	if len(labels) > 0 {
		only = map[string]bool{}
		reads := make([]store.HashRead, len(labels))
		for i, l := range labels {
			reads[i] = store.HashRead{Key: "s:" + *sprint + ":card:" + l, Fields: []string{"bench"}}
			only[l] = true
		}
		vals, err := st.PipelineHMGet(ctx, reads)
		if err != nil {
			return refuse(errOut, "card harvest", err.Error())
		}
		seen := map[string]bool{}
		for i, v := range vals {
			b, _ := v[0].(string)
			if b == "" {
				return refuse(errOut, "card harvest", "card "+labels[i]+" has no bench in sprint "+*sprint)
			}
			if !seen[b] {
				seen[b] = true
				benches = append(benches, b)
			}
		}
	}
	if *instance == "" {
		host, _ := os.Hostname()
		*instance = fmt.Sprintf("%s-%d", host, os.Getpid())
	}

	results := harvest.Run(ctx, st, harvest.Options{
		Sprint: *sprint, Benches: benches, Labels: only, Clock: *clock, Instance: *instance,
		Forge:  harvest.GitHub{Owner: *owner},
		Pusher: harvest.SSHPusher{},
	})
	code := 0
	for _, r := range results {
		for _, c := range r.Cards {
			fmt.Fprintf(out, "HARVESTED %s %s pr=%d head=%s via=%s bench=%s\n",
				*sprint, c.Label, c.PR, c.Head, c.Via, r.Bench)
		}
		for _, f := range r.Failed {
			fmt.Fprintf(out, "HARVEST-FAILED %s %s bench=%s err=%s\n", *sprint, f.Label, r.Bench, oneline.Escape(f.Err.Error()))
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
		fmt.Fprintln(out, line)
	}
	return code
}

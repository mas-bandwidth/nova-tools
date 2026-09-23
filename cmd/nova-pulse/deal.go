package main

// The deal verb: the dealer (internal/pulse/dealer) as a command, so the decisions #3251
// took out of the fill are made somewhere an executable runs (Stella's hold on #3304: the
// fill's load brake went, and no production path called dealer.PlanDeal). One pass:
//
//   - reads the undealt cards (--priority first, then --undealt),
//   - reads each --bench's two Redis rows, `bench:<b>` (working, load1, ncpu) and
//     `bench:<b>:desired` (slots, legs), in ONE pipelined round trip; a bench with no
//     `bench:<b>` row is down,
//   - counts what already waits in each bench's ready queue (<ready-root>/<bench>) and takes
//     the live lanes from those cards and from every --launched directory,
//   - plans with dealer.PlanDeal under --max-load-per-core (the load ceiling the fill no
//     longer applies), --reading-debt/--debt-cap and the --route-table,
//   - and applies it with dealer.Apply: ROUTE: and MODEL: written on the card, the card
//     renamed into <ready-root>/<bench>, where `nova-pulse fill --ready <ready-root>/<bench>`
//     launches it without re-deciding anything.
//
// One DEAL line per pass, one DEALT line per card moved, one HELD line per card kept with
// its reason. --dry-run plans and prints and moves nothing.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/pulse/dealer"
)

// defaultMaxLoadPerCore is the ceiling the fill's brake used (1.5), now the dealer's.
const defaultMaxLoadPerCore = 1.5

// dirsFlag is a repeatable directory flag.
type dirsFlag []string

func (d *dirsFlag) String() string { return strings.Join(*d, ",") }

func (d *dirsFlag) Set(v string) error {
	if s := strings.TrimSpace(v); s != "" {
		*d = append(*d, s)
	}
	return nil
}

func cmdDeal(args []string, stdout, stderr io.Writer) int {
	f := newFlags("deal")
	undealt := f.fs.String("undealt", "", "")
	priority := f.fs.String("priority", "", "")
	readyRoot := f.fs.String("ready-root", "", "")
	addr := f.fs.String("redis", "", "")
	routeTable := f.fs.String("route-table", "", "")
	maxLoad := f.fs.Float64("max-load-per-core", defaultMaxLoadPerCore, "")
	debt := f.fs.Int("reading-debt", 0, "")
	debtCap := f.fs.Int("debt-cap", 0, "")
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "dev", "")
	results := f.fs.String("results", "", "")
	dry := f.fs.Bool("dry-run", false, "")
	var benches benchFlag
	var launched dirsFlag
	f.fs.Var(&benches, "bench", "")
	f.fs.Var(&launched, "launched", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*undealt, "undealt", "the directory holding the card-<n>.md not yet dealt")
	f.want(*readyRoot, "ready-root", "the directory whose <bench>/ subdirectories are the benches' ready queues")
	f.want(*addr, "redis", "the Redis address carrying bench:<b> and bench:<b>:desired")
	f.want(*routeTable, "route-table", "the route table, <kind>\\t<route>\\t<model> per line")
	if len(benches) == 0 {
		f.add("--bench is required (repeatable or comma separated); it wants the benches to deal to; refusing to guess")
	}
	if *maxLoad < 0 {
		f.add(fmt.Sprintf("--max-load-per-core is 0 (no ceiling) or more, got %g", *maxLoad))
	}
	if f.refused(stderr) {
		return 2
	}
	router, err := dealer.ReadRouteTable(*routeTable)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse deal: %s\n", err)
		return 2
	}
	var cards []dealer.Card
	for _, src := range []struct {
		dir  string
		prio bool
	}{{*priority, true}, {*undealt, false}} {
		if src.dir == "" {
			continue
		}
		cs, err := readDealCards(src.dir, src.prio)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse deal: %s\n", err)
			return 1
		}
		cards = append(cards, cs...)
	}
	rows, err := readBenchRows(*addr, benches)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse deal: redis %s: %s\n", oneline.Field(*addr), err)
		return 1
	}
	readyDir := func(b string) string { return filepath.Join(*readyRoot, b) }
	live := map[string]string{}
	var bs []dealer.Bench
	for _, b := range benches {
		row, desired := rows[b][0], rows[b][1]
		bench := dealer.BenchFromRows(b, len(row) > 0, row, desired)
		waiting, err := readDealCards(readyDir(b), false)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "nova-pulse deal: %s\n", err)
			return 1
		}
		// The ready queue on disk is what waits; a row's dealer_queue is only a copy of it.
		if len(waiting) > bench.Queued {
			bench.Queued = len(waiting)
		}
		addLanes(live, waiting)
		bs = append(bs, bench)
	}
	for _, dir := range launched {
		cs, err := readDealCards(dir, false)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "nova-pulse deal: %s\n", err)
			return 1
		}
		addLanes(live, cs)
	}
	var deps dealer.Deps
	if *repo != "" {
		deps = pulseDeps(*repo, *base, *results)
	}
	plan := dealer.PlanDeal(cards, bs, live, dealer.Policy{
		MaxLoadPerCore: *maxLoad,
		ReadingDebt:    *debt,
		DebtCap:        *debtCap,
	}, deps, router)
	done := plan.Deals
	if !*dry {
		if done, err = dealer.Apply(plan, readyDir); err != nil {
			fmt.Fprintf(stderr, "nova-pulse deal: %s\n", err)
			return 1
		}
	}
	for _, d := range done {
		fmt.Fprintf(stdout, "DEALT card=%s bench=%s route=%s model=%s\n",
			oneline.Field(d.Card.Name), oneline.Field(d.Bench), oneline.Field(d.Route), oneline.Field(d.Model))
	}
	for _, h := range plan.Held {
		fmt.Fprintf(stdout, "HELD card=%s reason=%q\n", oneline.Field(h.Card.Name), h.Reason)
	}
	fmt.Fprintf(stdout, "DEAL dealt=%d held=%d cards=%d benches=%d max-load-per-core=%g dry-run=%v\n",
		len(done), len(plan.Held), len(cards), len(bs), *maxLoad, *dry)
	return 0
}

// readDealCards reads every card-<n>.md in dir, in name order.
func readDealCards(dir string, priority bool) ([]dealer.Card, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "card-*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []dealer.Card
	for _, p := range paths {
		c, err := dealer.ReadCard(p, priority)
		if err != nil {
			continue // taken under us
		}
		out = append(out, c)
	}
	return out, nil
}

func addLanes(live map[string]string, cards []dealer.Card) {
	for _, c := range cards {
		if c.Lane != "" {
			if _, ok := live[c.Lane]; !ok {
				live[c.Lane] = c.Name
			}
		}
	}
}

// readBenchRows reads bench:<b> and bench:<b>:desired for every bench in one pipeline.
func readBenchRows(addr string, benches []string) (map[string][2]map[string]string, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pipe := rdb.Pipeline()
	cmds := make([][2]*redis.MapStringStringCmd, len(benches))
	for i, b := range benches {
		cmds[i] = [2]*redis.MapStringStringCmd{pipe.HGetAll(ctx, "bench:"+b), pipe.HGetAll(ctx, "bench:"+b+":desired")}
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := map[string][2]map[string]string{}
	for i, b := range benches {
		out[b] = [2]map[string]string{cmds[i][0].Val(), cmds[i][1].Val()}
	}
	return out, nil
}

// pulseDeps is the real DEPENDS-ON checker: the results store and the base branch.
func pulseDeps(repo, base, results string) dealer.Deps {
	return pulse.NewGitAndResultsChecker(repo, base, results)
}

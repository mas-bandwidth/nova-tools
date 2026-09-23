// The fill and width verbs (nova-tools #3071). `fill --as <f>` is what a
// harness that spawns by hand (the coordinator's session) runs on an
// UNDERFULL wake: it reserves exactly the ready ids the friend may start now
// and prints one per line with its brief, so the spawner starts one child per
// line with no judgement. `width` runs one width tick (desired, deficit, CAP,
// underfull rebalance, READ-BOUND, the coordinator's wake) and prints its
// lines. The tick has one writer: the reconciler pass runs it every pass
// (width.Duty, `nova-sprint reconcile --width-rebalance-ticks n`), so `width`
// takes lease:reconciler for its one tick and refuses (exit 2) while a
// reconciler holds it.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
)

func init() {
	register(Verb{
		Name:    "fill",
		Summary: "reserve and print the ready ids a friend may start now, one per line",
		Run:     runFill,
	})
	register(Verb{
		Name:    "width",
		Summary: "one width tick: working/desired per friend, CAP, underfull moves, READ-BOUND",
		Run:     runWidth,
	})
}

func runFill(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("fill")
	addr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	dry := fs.Bool("dry", false, "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fill", err.Error())
	}
	if *as == "" {
		return refuse(errOut, "fill", "--as is required")
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fill", "takes no arguments")
	}
	if *actor == "" {
		*actor = *as
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "fill", err.Error())
	}
	defer st.Close()
	if *dry {
		_, _, ids, err := width.ReadyIDs(ctx, st, *as)
		if err != nil {
			return refuse(errOut, "fill", err.Error())
		}
		for _, r := range ids {
			brief := r.Brief
			if brief == "" {
				brief = "-"
			}
			fmt.Fprintf(out, "%s %s sprint=%s\n", r.ID, brief, r.Sprint)
		}
		return 0
	}
	reserved, err := width.Fill(ctx, st, *as, *actor, *idem)
	for _, r := range reserved {
		fmt.Fprintln(out, r.Line())
	}
	if err != nil {
		return refuse(errOut, "fill", err.Error())
	}
	return 0
}

func runWidth(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("width")
	addr := fs.String("redis", "", "")
	ticks := fs.Int("rebalance-ticks", 0, "")
	readers := fs.String("readers", "", "")
	builders := fs.String("builders", "", "")
	coordinator := fs.String("coordinator", "", "")
	actor := fs.String("actor", "width", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "width", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "width", "takes no arguments")
	}
	if *ticks < 1 {
		return refuse(errOut, "width", "--rebalance-ticks is required: the measured p95 take latency in ticks")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}
	defer st.Close()
	host, _ := os.Hostname()
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: host, Instance: "width-verb"})
	var held *reconcile.HeldError
	if errors.As(err, &held) {
		fmt.Fprintf(out, "REFUSED width: the reconciler ticks width every pass; %s\n", held.Error())
		return 2
	}
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}
	res, err := width.Tick(ctx, st, width.Policy{
		RebalanceTicks: *ticks,
		Readers:        splitNames(*readers),
		Builders:       splitNames(*builders),
		Coordinator:    *coordinator,
	}, lease.Token(), *actor, *idem)
	if rerr := lease.Release(context.Background()); err == nil && rerr != nil {
		err = rerr
	}
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}
	for _, r := range res.Rows {
		fmt.Fprintln(out, r.Line())
	}
	for _, e := range res.Events {
		fmt.Fprintln(out, e)
	}
	fmt.Fprintln(out, res.Fleet())
	return 0
}

func splitNames(csv string) []string {
	var out []string
	for _, name := range strings.Split(csv, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

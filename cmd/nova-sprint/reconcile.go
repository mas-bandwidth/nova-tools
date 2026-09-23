// The reconcile verb runs the reconciler (#2756 section 5, nova-tools #2726):
// one instance fleet-wide, held by lease:reconciler with a random instance id
// and a fencing token, one pass per second. It registers through registry.go.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
)

func init() {
	register(Verb{
		Name:    "reconcile",
		Summary: "run the reconciler: hold lease:reconciler, one pass per second (exit 2 held, 3 fenced, 6 no Redis)",
		Run:     runReconcile,
	})
}

// runReconcile takes the lease or refuses, then passes until SIGTERM/SIGINT
// (release, exit 0) or until another instance fences it (exit 3, no release:
// the lease is no longer ours). --once runs one pass and releases.
func runReconcile(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("reconcile")
	redisAddr := fs.String("redis", "", "")
	host := fs.String("host", "", "")
	once := fs.Bool("once", false, "")
	widthTicks := fs.Int("width-rebalance-ticks", 0, "")
	widthReaders := fs.String("width-readers", "", "")
	widthBuilders := fs.String("width-builders", "", "")
	widthCoordinator := fs.String("width-coordinator", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "reconcile", "takes flags, not positional arguments: --redis <addr> [--host <name>] [--once] [--width-rebalance-ticks n --width-readers a,b --width-builders c,d --width-coordinator e]")
	}
	if *host == "" {
		h, err := os.Hostname()
		if err != nil {
			return refuse(errOut, "reconcile", "no --host and no hostname: "+err.Error())
		}
		*host = h
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint reconcile: %v\n", err)
		return 6
	}
	defer st.Close()

	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: *host})
	var held *reconcile.HeldError
	if errors.As(err, &held) {
		fmt.Fprintf(out, "REFUSED reconcile %s\n", held.Error())
		return 2
	}
	if err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	fmt.Fprintf(out, "RECONCILER instance=%s host=%s token_sha=%s ttl=%s\n",
		lease.Instance(), lease.Host(), lease.TokenSHA(), lease.TTL())

	loop := &reconcile.Loop{
		Lease:   lease,
		OnError: func(err error) { fmt.Fprintf(errOut, "nova-sprint reconcile: pass: %v\n", err) },
	}
	// The width duty (#3071, #3086): one width tick per pass under the lease,
	// and a completion's replacement dealt in the same pass. It needs the
	// measured rebalance ticks (p95 take latency); without them it is off and
	// the banner says so.
	if *widthTicks > 0 {
		duty := &width.Duty{Store: st, Policy: width.Policy{
			RebalanceTicks: *widthTicks,
			Readers:        splitNames(*widthReaders),
			Builders:       splitNames(*widthBuilders),
			Coordinator:    *widthCoordinator,
		}}
		loop.Duties = append(loop.Duties, duty.Run)
		fmt.Fprintf(out, "WIDTH on rebalance_ticks=%d\n", *widthTicks)
	} else {
		fmt.Fprintln(out, "WIDTH off: no --width-rebalance-ticks")
	}
	if *once {
		loop.Passes = 1
	}
	err = loop.Run(ctx)
	if errors.Is(err, reconcile.ErrFenced) {
		fmt.Fprintf(out, "FENCED reconcile %v\n", err)
		return 3
	}
	if err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	// Release on a fresh context: ctx is already cancelled on SIGTERM.
	if err := lease.Release(context.Background()); err != nil {
		if errors.Is(err, reconcile.ErrFenced) {
			fmt.Fprintf(out, "FENCED reconcile %v\n", err)
			return 3
		}
		return refuse(errOut, "reconcile", "release: "+err.Error())
	}
	fmt.Fprintf(out, "RELEASED reconcile instance=%s\n", lease.Instance())
	return 0
}

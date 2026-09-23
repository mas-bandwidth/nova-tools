// The reconcile verb runs the reconciler (#2756 section 5, nova-tools #2726):
// one instance fleet-wide, held by lease:reconciler with a random instance id
// and a fencing token, one pass per second. It registers through registry.go.
//
// Every pass runs the production duties under the lease: the refill (#2935),
// which deals on stream events and on the 10 s sweep, then each duty another
// package registered through registerReconcileDuty (the width tick of #3071
// plugs in there without this file importing it).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "reconcile",
		Summary: "run the reconciler: hold lease:reconciler, one pass per second (exit 2 held, 3 fenced, 6 no Redis)",
		Run:     runReconcile,
	})
}

// reconcileDuty is one duty the production loop runs every pass after the
// lease renew. *reconcile.Refill has this Run; so does the width tick's
// width.Duty (#3071, #3086), which registers itself here.
type reconcileDuty interface {
	Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error)
}

// reconcileDutyBuilder builds one registered duty for this instance over its
// store. A nil duty (with a nil error) is a duty switched off by config.
type reconcileDutyBuilder struct {
	Name  string
	Build func(st *store.Store) (reconcileDuty, error)
}

// reconcileDuties are the duties other packages registered, run after the
// refill in registration order.
var reconcileDuties []reconcileDutyBuilder

// registerReconcileDuty adds a duty to every `nova-sprint reconcile` loop.
func registerReconcileDuty(name string, build func(st *store.Store) (reconcileDuty, error)) {
	reconcileDuties = append(reconcileDuties, reconcileDutyBuilder{Name: name, Build: build})
}

// reconcileSeams are the deal pass's two host seams: the system ssh to each
// bench and the forge by `gh api` REST. A test swaps in its fixture sshd and
// forge map (CI-NET: no host in a test); nothing else in the loop changes.
var reconcileSeams = func() (deal.Dialer, deal.PRs) { return deal.Remote{}, deal.GH{} }

// productionDuties is the loop's duty list: the refill and its deal pass over
// Redis, then every registered duty. It returns the names in order.
func productionDuties(st *store.Store) ([]reconcile.Duty, []string, error) {
	dialer, prs := reconcileSeams()
	refill := &reconcile.Refill{
		Client: st.Client(),
		Deal:   &deal.Pass{Dialer: dialer, PRs: prs},
	}
	duties := []reconcile.Duty{refill.Run}
	names := []string{"refill"}
	for _, b := range reconcileDuties {
		d, err := b.Build(st)
		if err != nil {
			return nil, nil, fmt.Errorf("duty %s: %w", b.Name, err)
		}
		if d == nil {
			continue
		}
		duties = append(duties, d.Run)
		names = append(names, b.Name)
	}
	return duties, names, nil
}

// runReconcile takes the lease or refuses, then passes until SIGTERM/SIGINT
// (release, exit 0) or until another instance fences it (exit 3, no release:
// the lease is no longer ours). --once runs one pass and releases.
func runReconcile(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("reconcile")
	redisAddr := fs.String("redis", "", "")
	host := fs.String("host", "", "")
	once := fs.Bool("once", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "reconcile", "takes flags, not positional arguments: --redis <addr> [--host <name>] [--once]")
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

	duties, names, err := productionDuties(st)
	if err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}

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

	fmt.Fprintf(out, "DUTIES %s\n", strings.Join(names, ","))

	loop := &reconcile.Loop{
		Lease:   lease,
		Duties:  duties,
		OnError: func(err error) { fmt.Fprintf(errOut, "nova-sprint reconcile: pass: %v\n", err) },
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

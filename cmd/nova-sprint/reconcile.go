// The reconcile verb runs the reconciler (#2756 section 5, nova-tools #2726):
// one instance fleet-wide, held by lease:reconciler with a random instance id
// and a fencing token, one pass per second. It registers through registry.go.
//
// Every pass runs the production duties under the lease: the refill (#2935),
// which deals on stream events and on the 10 s sweep, then each duty another
// package registered through registerReconcileDuty (the width tick of #3071
// plugs in there without this file importing it).
//
// --metrics-addr <host:port> serves /metrics (internal/metrics, nx-g61 #2720)
// for as long as the verb runs: the deal pass exports the cards still pooled,
// the slots leased over every bench and one ssh session latency per bench.
// Empty, the default, serves nothing.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "reconcile",
		Summary: "run the reconciler: hold lease:reconciler, one pass per second (exit 1 --once duty error, 2 held, 3 fenced, 6 no Redis)",
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
// Redis, then every registered duty. It returns the names in order. set
// receives the deal pass's metrics (nx-g61); nil exports nothing.
func productionDuties(st *store.Store, set *metrics.Set) ([]reconcile.Duty, []string, error) {
	dialer, prs := reconcileSeams()
	refill := &reconcile.Refill{
		Client: st.Client(),
		Deal:   &deal.Pass{Dialer: dialer, PRs: prs, Metrics: set},
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
// the lease is no longer ours). --once runs one pass and releases, then exits
// 1 when any duty errored. Every duty error is printed with the duty's name.
func runReconcile(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("reconcile")
	redisAddr := fs.String("redis", "", "")
	host := fs.String("host", "", "")
	once := fs.Bool("once", false, "")
	widthTicks := fs.Int("width-rebalance-ticks", 0, "")
	widthReaders := fs.String("width-readers", "", "")
	widthBuilders := fs.String("width-builders", "", "")
	widthCoordinator := fs.String("width-coordinator", "", "")
	metricsAddr := fs.String("metrics-addr", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "reconcile", "takes flags, not positional arguments: --redis <addr> [--host <name>] [--once] [--width-rebalance-ticks n --width-readers a,b --width-builders c,d --width-coordinator e] [--metrics-addr <host:port>]")
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

	duties, names, err := productionDuties(st, metrics.Default)
	if err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	if strings.TrimSpace(*metricsAddr) != "" {
		srv, err := metrics.Default.Listen(*metricsAddr)
		if err != nil {
			return refuse(errOut, "reconcile", "--metrics-addr "+strconv.Quote(*metricsAddr)+": "+err.Error()+" (name a free host:port, or leave it out)")
		}
		defer srv.Close()
		fmt.Fprintf(out, "METRICS reconcile url=%s\n", srv.URL())
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

	named := &namedDuties{errOut: errOut}
	loop := &reconcile.Loop{
		Lease:   lease,
		Duties:  named.wrap(duties, names),
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
	if *once && named.failed() {
		// --once is a probe: a duty error is its answer, not a pass note
		// (#3321: the deal pass failed every card and --once exited 0).
		return 1
	}
	return 0
}

// namedDuties wraps each duty so its error reaches stderr under the duty's
// name, not only proc:reconciler err (nova-tools #3321: Loop.OnError sees pass
// errors, never duty errors). A duty's error is printed when it first appears
// or changes, so a long-running loop does not repeat it every second. A fence
// passes through untouched: the loop stops on it.
type namedDuties struct {
	errOut io.Writer
	mu     sync.Mutex
	last   map[string]string // duty name -> its last error text ("" when clean)
	any    bool              // any duty errored in any pass
}

func (n *namedDuties) wrap(duties []reconcile.Duty, names []string) []reconcile.Duty {
	n.last = map[string]string{}
	out := make([]reconcile.Duty, len(duties))
	for i, d := range duties {
		d, name := d, names[i]
		out[i] = func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
			c, err := d(ctx, l)
			n.note(name, err)
			return c, err
		}
	}
	return out
}

func (n *namedDuties) note(name string, err error) {
	if errors.Is(err, reconcile.ErrFenced) {
		return
	}
	text := ""
	if err != nil {
		text = oneline.Escape(err.Error())
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.any = true
	}
	if n.last[name] == text {
		return
	}
	n.last[name] = text
	if err != nil {
		fmt.Fprintf(n.errOut, "nova-sprint reconcile: duty %s: %s\n", name, text)
	}
}

func (n *namedDuties) failed() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.any
}

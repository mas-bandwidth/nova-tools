// The reconcile verb runs the reconciler (#2756 section 5, nova-tools #2726):
// one instance fleet-wide, held by lease:reconciler with a random instance id
// and a fencing token, one pass per second. It registers through registry.go.
//
// Every pass runs the production duties under the lease: the refill (#2935),
// which deals on stream events and on the 10 s sweep, then each duty another
// file registered through registerReconcileDuty: ok-to-friend and harvest
// (consume.go), expire (expire.go), route (route_duty.go, #3323: reads, fixes
// and merging over every open sprint). After each pass a `DUTY <name>` line
// says what each duty did (#3199: every duty under --once, a moving or
// failing one in the loop).
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
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
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

// stoppableDuty is a duty with work of its own past its Run (the harvest
// workers, #3737): Stop ends it before the verb exits, until ctx ends.
type stoppableDuty interface {
	Stop(ctx context.Context) []string
}

// productionDuties is the loop's duty list: the refill and its deal pass over
// Redis, then every registered duty. It returns the names in order. set
// receives the deal pass's metrics (nx-g61); nil exports nothing. stops are
// the Stop of every duty with work of its own past its Run, for the way out.
func productionDuties(st *store.Store, set *metrics.Set) (duties []reconcile.Duty, names []string, stops []stoppableDuty, err error) {
	dialer, prs := reconcileSeams()
	refill := &reconcile.Refill{
		Client: st.Client(),
		Deal:   &deal.Pass{Dialer: dialer, PRs: prs, Metrics: set},
	}
	fleetRefill := func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
		if err := fleet.Step(ctx, st.Client()); err != nil {
			return reconcile.Counts{}, err
		}
		return refill.Run(ctx, l)
	}
	duties = []reconcile.Duty{fleetRefill}
	names = []string{"refill"}
	for _, b := range reconcileDuties {
		d, err := b.Build(st)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("duty %s: %w", b.Name, err)
		}
		if d == nil {
			continue
		}
		duties = append(duties, d.Run)
		names = append(names, b.Name)
		if s, ok := d.(stoppableDuty); ok {
			stops = append(stops, s)
		}
	}
	return duties, names, stops, nil
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
	readers := fs.String("readers", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "reconcile", "takes flags, not positional arguments: --redis <addr> [--host <name>] [--once] [--readers a,b] [--width-rebalance-ticks n --width-readers a,b --width-builders c,d --width-coordinator e] [--metrics-addr <host:port>]")
	}
	reconcileReaders = splitNames(*readers)
	reconcileOut = out
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

	duties, names, stops, err := productionDuties(st, metrics.Default)
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

	// The lease heartbeats itself every TTL/3 from here until Release
	// (#3737), whatever the duties are doing.
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: *host, Heartbeat: true})
	var held *reconcile.HeldError
	if errors.As(err, &held) {
		fmt.Fprintf(out, "REFUSED reconcile %s\n", held.Error())
		return 2
	}
	if err != nil {
		return refuse(errOut, "reconcile", err.Error())
	}
	fmt.Fprintf(out, "RECONCILER instance=%s host=%s token_sha=%s ttl=%s heartbeat=%s\n",
		lease.Instance(), lease.Host(), lease.TokenSHA(), lease.TTL(), lease.Heartbeat())

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
		}, PRs: &deal.GH{}}
		duties = append(duties, duty.Run)
		names = append(names, "width")
	}

	fmt.Fprintf(out, "DUTIES %s\n", strings.Join(names, ","))

	named := &namedDuties{errOut: errOut}
	loop := &reconcile.Loop{
		Lease:   lease,
		Duties:  named.wrap(duties, names),
		OnError: func(err error) { fmt.Fprintf(errOut, "%s nova-sprint reconcile: pass: %v\n", logStamp(), err) },
		// Per-duty receipts (#3199): every pass under --once, so the probe
		// says what each duty did; in the loop only a duty that moved
		// something or failed, so an idle second prints nothing.
		AfterPass: func(reconcile.PassResult) { named.report(out, *once) },
	}
	if *widthTicks > 0 {
		fmt.Fprintf(out, "WIDTH on rebalance_ticks=%d\n", *widthTicks)
	} else {
		fmt.Fprintln(out, "WIDTH off: no --width-rebalance-ticks")
	}
	if *once {
		loop.Passes = 1
	}
	err = loop.Run(ctx)
	// Stop the duties' own work before anything else (#3737): each harvest
	// pass records its pass line (err=FENCED when fenced) and gives back its
	// lease:harvest:<b>, so no bench is left held by this instance.
	stopDuties(out, stops)
	if errors.Is(err, reconcile.ErrFenced) {
		lease.StopHeartbeat()
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

// reconcileStopWait bounds how long the verb waits on its way out for the
// duties' own work to record itself (each harvest pass's line and release).
var reconcileStopWait = 5 * time.Second

// stopDuties stops every stoppable duty, each bounded by reconcileStopWait,
// and prints one `HARVEST RELEASED bench=<b>` line per bench lease released
// by its token because its worker did not record in time.
func stopDuties(out io.Writer, stops []stoppableDuty) {
	for _, s := range stops {
		ctx, cancel := context.WithTimeout(context.Background(), reconcileStopWait)
		for _, b := range s.Stop(ctx) {
			fmt.Fprintf(out, "HARVEST RELEASED bench=%s by token on the way out\n", b)
		}
		cancel()
	}
}

// report prints one `DUTY <name> <counts> err=<text>` line per duty of the
// pass just recorded, in duty order; all of them when all is set, else only
// the duties that moved something or errored.
func (n *namedDuties) report(out io.Writer, all bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, name := range n.names {
		c, e := n.counts[name], n.last[name]
		if !all && c.Zero() && e == "" {
			continue
		}
		fmt.Fprintf(out, "DUTY %s %s err=%s\n", name, c.Line(), e)
	}
	n.counts = map[string]reconcile.Counts{}
}

// namedDuties wraps each duty so its error reaches stderr under the duty's
// name, not only proc:reconciler err (nova-tools #3321: Loop.OnError sees pass
// errors, never duty errors). A duty's error is printed when it first appears
// or changes, so a long-running loop does not repeat it every second. A fence
// passes through untouched: the loop stops on it.
type namedDuties struct {
	errOut io.Writer
	mu     sync.Mutex
	names  []string                    // duty names in pass order
	counts map[string]reconcile.Counts // duty name -> its counts in the current pass
	last   map[string]string           // duty name -> its last error text ("" when clean)
	any    bool                        // any duty errored in any pass
}

func (n *namedDuties) wrap(duties []reconcile.Duty, names []string) []reconcile.Duty {
	n.names = names
	n.last = map[string]string{}
	n.counts = map[string]reconcile.Counts{}
	out := make([]reconcile.Duty, len(duties))
	for i, d := range duties {
		d, name := d, names[i]
		out[i] = func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
			c, err := d(ctx, l)
			n.note(name, c, err)
			return c, err
		}
	}
	return out
}

func (n *namedDuties) note(name string, c reconcile.Counts, err error) {
	if errors.Is(err, reconcile.ErrFenced) {
		return
	}
	text := ""
	if err != nil {
		text = oneline.Escape(err.Error())
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.counts[name] = c
	if err != nil {
		n.any = true
	}
	if n.last[name] == text {
		return
	}
	n.last[name] = text
	if err != nil {
		fmt.Fprintf(n.errOut, "%s nova-sprint reconcile: duty %s: %s\n", logStamp(), name, text)
	}
}

// reconcileClock is the log lines' clock; a test pins it.
var reconcileClock = time.Now

// logStamp leads every line the running loop writes to stderr (#3620: a
// "Function not found" line with no time could not be placed before or after
// a restart). UTC, milliseconds, RFC 3339.
func logStamp() string {
	return reconcileClock().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func (n *namedDuties) failed() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.any
}

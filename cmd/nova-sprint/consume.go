// The consume verb and the consumer duties (nova-tools #3323): each built
// consumer of #2756 4.5 runs under its own verb
//
//	nova-sprint consume <group> once|run --redis <addr> [--sprint <S>] [--bench <b>[,<b>...]] [--consumer <id>] [--every 1s]
//	nova-sprint consume list
//
// and as a production duty of `nova-sprint reconcile`, so the fleet runs it
// with no unit of its own: ok-to-friend (#2933) passes every open sprint's
// log inside the 1 s reconciler tick without blocking; harvest (#2932) starts
// one pass per idle bench over every open sprint, each under its own
// lease:harvest:<b> and clock, off the reconciler's goroutine. Each writes its
// own proc line (proc:ok-to-friend, proc:harvest:<b>).
//
// pr-to-read passes every open sprint's log too (prReadDuty), each under its
// own lease:route:<S>, joining a sprint opened after the reconciler started
// on the next pass. hold-to-fix (#3092) is not built on dev: its verb
// refuses and names the issue, and `consume list` says so.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Consumer groups, in `consume list` order.
const (
	groupHarvest   = "harvest"
	groupPRToRead  = "pr-to-read"
	groupHoldToFix = "hold-to-fix"
)

// consumeNotBuilt names each consumer whose handler is not on dev yet and the
// issue that builds it.
var consumeNotBuilt = map[string]string{
	groupHoldToFix: "#3092",
}

// consumePRReadRemote is the remote seam for pr-to-read, swapped in tests.
var consumePRReadRemote consume.Remote = consume.GitRemote

// consumeHarvestLog receives the harvest duty's receipt lines (TAKEN
// from=<instance> stale, #3737): the reconciler's stdout, its log.
var consumeHarvestLog = func(line string) { fmt.Println(line) }

// consumeHarvestSeams are the harvest's two host seams: GitHub by `gh api`
// REST and the push over ssh from the bench. A test swaps in fixtures
// (CI-NET: no host in a test).
var consumeHarvestSeams = func() (harvest.Forge, harvest.Pusher) {
	return harvest.GitHub{Owner: "mas-bandwidth"}, harvest.SSHPusher{}
}

func init() {
	register(Verb{
		Name:    "consume",
		Summary: "run one consumer (ok-to-friend, harvest, pr-to-read; hold-to-fix once #3092 lands): consume <group> once|run --redis <addr>, or consume list",
		Run:     runConsume,
	})
	registerReconcileDuty(consume.GroupOkFriend, func(st *store.Store) (reconcileDuty, error) {
		return &okFriendDuty{st: st, started: map[string]bool{}}, nil
	})
	registerReconcileDuty(groupHarvest, func(st *store.Store) (reconcileDuty, error) {
		forge, pusher := consumeHarvestSeams()
		return &harvestDuty{st: st, forge: forge, pusher: pusher, busy: map[string]bool{}, log: consumeHarvestLog}, nil
	})
	registerReconcileDuty(groupPRToRead, func(st *store.Store) (reconcileDuty, error) {
		return &prReadDuty{st: st, out: consumePRReadOut}, nil
	})
}

// consumePRReadOut receives the pr-to-read duty's receipt lines (PRREAD
// JOINED, READ QUEUED): the reconciler's stdout, its log.
var consumePRReadOut io.Writer = os.Stdout

// Exit 0 every pass finished (an event left pending for want of readers is
// not a failure; its line says PENDING); 1 a pass failed; 2 usage or a
// consumer not built; 6 no Redis.
func runConsume(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "consume", "wants a group (ok-to-friend, harvest, pr-to-read, hold-to-fix) and once|run, or list")
	}
	group := args[0]
	if group == "list" {
		fmt.Fprintf(out, "%s duty=reconcile verb=consume proc=proc:%s\n", consume.GroupOkFriend, consume.GroupOkFriend)
		fmt.Fprintf(out, "%s duty=reconcile verb=consume proc=proc:harvest:<bench>\n", groupHarvest)
		fmt.Fprintf(out, "%s duty=reconcile,route verb=consume-once proc=proc:%s\n", groupPRToRead, groupPRToRead)
		for _, g := range []string{groupHoldToFix} {
			fmt.Fprintf(out, "%s not-built=%s\n", g, consumeNotBuilt[g])
		}
		return 0
	}
	if issue, ok := consumeNotBuilt[group]; ok {
		return refuse(errOut, "consume", fmt.Sprintf("%s is not built on dev (its handler is %s); nothing runs under this name yet", group, issue))
	}
	if group != consume.GroupOkFriend && group != groupHarvest && group != groupPRToRead {
		return refuse(errOut, "consume", "unknown group "+group+"; want ok-to-friend, harvest, pr-to-read or hold-to-fix")
	}
	if len(args) < 2 || (args[1] != "once" && args[1] != "run") {
		return refuse(errOut, "consume "+group, "wants once or run")
	}
	mode := args[1]
	fs := taskFlags("consume " + group)
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	var benches listFlag
	fs.Var(&benches, "bench", "")
	consumer := fs.String("consumer", "", "")
	actor := fs.String("actor", group, "")
	every := fs.Duration("every", time.Second, "")
	if err := fs.Parse(args[2:]); err != nil {
		return refuse(errOut, "consume "+group, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "consume "+group, "takes flags after once|run, not positional arguments")
	}
	if *every <= 0 {
		return refuse(errOut, "consume "+group, "--every must be above zero")
	}
	if group == groupPRToRead {
		if *sprint == "" {
			return refuse(errOut, "consume "+group, "--sprint is required")
		}
		if mode == "run" {
			return refuse(errOut, "consume "+group+" run", "pr-to-read runs under nova-sprint route --sprint <S>; consume pr-to-read takes once")
		}
	}
	if *consumer == "" {
		host, _ := os.Hostname()
		*consumer = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint consume %s: %v\n", group, err)
		return 6
	}
	defer st.Close()

	if group == groupPRToRead {
		pr := &consume.PRRead{
			Store:    st,
			Sprint:   *sprint,
			Consumer: *consumer,
			Actor:    *actor,
			Remote:   consumePRReadRemote,
		}
		err := pr.Once(ctx)
		var held *consume.LeaseHeldError
		switch {
		case errors.As(err, &held):
			fmt.Fprintf(errOut, "REFUSED pr-to-read sprint=%s %s held by %s at %s\n", *sprint, consume.LeaseKey(*sprint), held.Holder, held.At)
			return 1
		case err != nil:
			fmt.Fprintf(errOut, "nova-sprint consume %s: %v\n", group, err)
			return 1
		}
		fmt.Fprintf(out, "CONSUMED pr-to-read sprint=%s\n", *sprint)
		return 0
	}

	var pass func(ctx context.Context, quiet bool) int
	switch group {
	case consume.GroupOkFriend:
		started := map[string]bool{}
		pass = func(ctx context.Context, quiet bool) int {
			return okFriendPass(ctx, st, *sprint, *consumer, *actor, started, out, quiet)
		}
	case groupHarvest:
		forge, pusher := consumeHarvestSeams()
		pass = func(ctx context.Context, quiet bool) int {
			return harvestPass(ctx, st, *sprint, benches, *consumer, forge, pusher, out, quiet)
		}
	}
	if mode == "once" {
		return pass(ctx, false)
	}
	fmt.Fprintf(out, "CONSUME %s run consumer=%s every=%s\n", group, *consumer, *every)
	tick := time.NewTicker(*every)
	defer tick.Stop()
	for ctx.Err() == nil {
		pass(ctx, true)
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	fmt.Fprintf(out, "CONSUME %s stopped consumer=%s\n", group, *consumer)
	return 0
}

// consumeSprints is --sprint when given, else every open sprint in the
// `sprints` set, in name order, read in one pipeline.
func consumeSprints(ctx context.Context, st *store.Store, only string) ([]string, error) {
	if only != "" {
		return []string{only}, nil
	}
	client := st.Client()
	names, err := client.SMembers(ctx, "sprints").Result()
	if err != nil {
		return nil, fmt.Errorf("sprints: %w", err)
	}
	sort.Strings(names)
	reads := make([]store.HashRead, len(names))
	for i, s := range names {
		reads[i] = store.HashRead{Key: "s:" + s, Fields: []string{"status"}}
	}
	vals, err := st.PipelineHMGet(ctx, reads)
	if err != nil {
		return nil, fmt.Errorf("sprint status: %w", err)
	}
	var open []string
	for i, v := range vals {
		if status, _ := v[0].(string); status == "open" {
			open = append(open, names[i])
		}
	}
	return open, nil
}

// consumeBenches is --bench when given, else every registered bench in name
// order.
func consumeBenches(ctx context.Context, st *store.Store, only []string) ([]string, error) {
	if len(only) > 0 {
		return only, nil
	}
	names, err := st.Client().SMembers(ctx, "benches").Result()
	if err != nil {
		return nil, fmt.Errorf("benches: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

func pendingOnReaders(err error) bool {
	return errors.Is(err, consume.ErrNoReaders) || errors.Is(err, consume.ErrReviewBlocked)
}

// okFriendPass passes each sprint's log once without blocking; started keeps
// the sprints whose group this consumer has created and reclaimed. quiet
// prints only a pass that handled or failed something.
func okFriendPass(ctx context.Context, st *store.Store, only, consumer, actor string, started map[string]bool, out io.Writer, quiet bool) int {
	sprints, err := consumeSprints(ctx, st, only)
	if err != nil {
		fmt.Fprintf(out, "CONSUME-FAILED ok-to-friend err=%s\n", oneline.Escape(err.Error()))
		return 1
	}
	if len(sprints) == 0 && !quiet {
		fmt.Fprintln(out, "CONSUMED ok-to-friend sprints=0")
	}
	code := 0
	for _, s := range sprints {
		o := &consume.OkFriend{Store: st, Sprint: s, Consumer: consumer, Actor: actor, Block: -1}
		if !started[s] {
			if err := o.Start(ctx); err != nil {
				fmt.Fprintf(out, "CONSUME-FAILED ok-to-friend sprint=%s err=%s\n", s, oneline.Escape(err.Error()))
				code = 1
				continue
			}
			started[s] = true
		}
		n, err := o.Pass(ctx)
		switch {
		case err == nil:
			if !quiet || n > 0 {
				fmt.Fprintf(out, "CONSUMED ok-to-friend sprint=%s n=%d\n", s, n)
			}
		case pendingOnReaders(err):
			fmt.Fprintf(out, "PENDING ok-to-friend sprint=%s n=%d err=%s\n", s, n, oneline.Escape(err.Error()))
		default:
			fmt.Fprintf(out, "CONSUME-FAILED ok-to-friend sprint=%s n=%d err=%s\n", s, n, oneline.Escape(err.Error()))
			code = 1
		}
	}
	return code
}

// harvestPass runs one harvest pass per sprint over the benches, every bench
// in parallel under its own lease and clock (harvest.Run).
func harvestPass(ctx context.Context, st *store.Store, only string, benchList []string, instance string,
	forge harvest.Forge, pusher harvest.Pusher, out io.Writer, quiet bool) int {
	sprints, err := consumeSprints(ctx, st, only)
	if err == nil {
		benchList, err = consumeBenches(ctx, st, benchList)
	}
	if err != nil {
		fmt.Fprintf(out, "CONSUME-FAILED harvest err=%s\n", oneline.Escape(err.Error()))
		return 1
	}
	code := 0
	for _, s := range sprints {
		results := harvest.Run(ctx, st, harvest.Options{
			Sprint: s, Benches: benchList, Instance: instance, Actor: "consume-harvest",
			Forge: forge, Pusher: pusher,
		})
		for _, r := range results {
			for _, c := range r.Cards {
				fmt.Fprintf(out, "HARVESTED %s %s pr=%d head=%s via=%s ci=%s bench=%s\n", s, c.Label, c.PR, c.Head, c.Via, c.CI, r.Bench)
			}
			for _, f := range r.Failed {
				fmt.Fprintf(out, "HARVEST-FAILED %s %s bench=%s err=%s%s\n", s, f.Label, r.Bench, oneline.Escape(f.Err.Error()), harvestFails(f))
				code = 1
			}
			if r.Err != nil {
				fmt.Fprintf(out, "HARVEST UNFINISHED %s bench=%s err=%s\n", s, r.Bench, oneline.Escape(r.Err.Error()))
				code = 1
			} else if !quiet && len(r.Failed) == 0 {
				fmt.Fprintf(out, "HARVEST OK %s bench=%s n=%d\n", s, r.Bench, len(r.Cards))
			}
		}
	}
	return code
}

// okFriendDuty is ok-to-friend as a reconcile duty: every pass, each open
// sprint's log once, without blocking, as consumer reconciler-<instance>. A
// new lease instance reclaims what a killed one left pending (Start). No
// sprint starts with less than the lease write margin left (#3805): the
// duty returns what it routed and names the sprints it left.
type okFriendDuty struct {
	st      *store.Store
	started map[string]bool // "<consumer>/<sprint>"
}

func (d *okFriendDuty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	var counts reconcile.Counts
	sprints, err := consumeSprints(ctx, d.st, "")
	if err != nil {
		return counts, fmt.Errorf("ok-to-friend: %w", err)
	}
	consumer := "reconciler-" + l.Instance()
	var errs []string
	for i, s := range sprints {
		if err := l.Bounded(0); errors.Is(err, reconcile.ErrFenced) {
			return counts, err
		} else if err != nil {
			prior := ""
			if len(errs) > 0 {
				prior = strings.Join(errs, "; ") + "; "
			}
			return counts, fmt.Errorf("ok-to-friend: %s%d of %d sprint(s) not started (%s): %w",
				prior, len(sprints)-i, len(sprints), strings.Join(sprints[i:], ","), err)
		}
		o := &consume.OkFriend{Store: d.st, Sprint: s, Consumer: consumer, Actor: "reconciler", Block: -1}
		if key := consumer + "/" + s; !d.started[key] {
			if err := o.Start(ctx); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", s, err))
				continue
			}
			d.started[key] = true
		}
		n, err := o.Pass(ctx)
		counts.Routed += n
		if err != nil && !pendingOnReaders(err) {
			errs = append(errs, fmt.Sprintf("%s: %v", s, err))
		}
	}
	if len(errs) > 0 {
		return counts, fmt.Errorf("ok-to-friend: %s", strings.Join(errs, "; "))
	}
	return counts, nil
}

// prReadDuty is pr-to-read as a reconcile duty: every pass, every open
// sprint in `sprints` (read again each pass, so a sprint opened after the
// reconciler started is joined on the next pass) gets one pr-to-read pass
// under its own lease:route:<S> (consume.PRReadSprints), as consumer
// reconciler-<instance>. A sprint a `nova-sprint route --sprint <S>` process
// serves is held by it and skipped. The passes run off the reconciler's
// goroutine, one at a time, since a pass may run git ls-remote; Run reports
// what the last finished pass moved and its error.
type prReadDuty struct {
	st  *store.Store
	out io.Writer

	mu      sync.Mutex
	all     *consume.PRReadSprints
	busy    bool
	moved   int
	lastErr error
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func (d *prReadDuty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	counts, err := reconcile.Counts{Routed: d.moved}, d.lastErr
	d.moved, d.lastErr = 0, nil
	if d.busy || l.Fenced() {
		return counts, err
	}
	if d.ctx == nil {
		d.ctx, d.cancel = context.WithCancel(ctx)
	}
	if d.ctx.Err() != nil {
		return counts, err
	}
	instance := "reconciler-" + l.Instance()
	if d.all == nil || d.all.Instance != instance {
		d.all = &consume.PRReadSprints{Store: d.st, Instance: instance, Actor: "reconciler",
			Remote: consumePRReadRemote, Out: d.out}
	}
	all, wctx := d.all, d.ctx
	d.busy = true
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		n, perr := all.Pass(wctx)
		d.mu.Lock()
		d.moved += n
		if perr != nil {
			d.lastErr = perr
		}
		d.busy = false
		d.mu.Unlock()
	}()
	return counts, err
}

// Stop cancels the pass in flight and waits for it until ctx ends; the pass
// gives back its lease:route:<S> on the way (PRRead.OnceN).
func (d *prReadDuty) Stop(ctx context.Context) []string {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
	waited := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
	}
	return nil
}

// harvestDuty is harvest as a reconcile duty: every pass, each registered
// bench with no pass in flight starts one, over every open sprint in order,
// on its own goroutine under lease:harvest:<b> and the harvest clock, so a
// slow push or REST call never holds the 1 s reconciler tick. A bench with
// nothing due costs three Redis Function calls and writes proc:harvest:<b>.
//
// Every pass is bounded by the reconciler lease (#3737): no card starts once
// the lease is fenced or less than the write margin of it is left, and the
// pass records n, took_ms and left=<k>. Stop is the way out: the reconciler
// exiting FENCED cancels every pass, waits for each to record its pass line
// (err=FENCED) and give back lease:harvest:<b>, and releases any lease a
// worker it could not wait for still holds, so no bench is left held by a
// dead instance until its TTL.
type harvestDuty struct {
	st     *store.Store
	forge  harvest.Forge
	pusher harvest.Pusher
	log    func(string)
	mu     sync.Mutex
	busy   map[string]bool
	held   map[string]string // bench -> the lease:harvest token its worker holds
	inst   string            // the lease holder name the workers use
	ctx    context.Context   // every worker's context; Stop cancels it
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (d *harvestDuty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	sprints, err := consumeSprints(ctx, d.st, "")
	if err == nil && len(sprints) > 0 {
		var benches []string
		benches, err = consumeBenches(ctx, d.st, nil)
		for _, b := range benches {
			d.start(ctx, l, "reconciler-"+l.Instance(), b, sprints)
		}
	}
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("harvest: %w", err)
	}
	return reconcile.Counts{}, nil
}

func (d *harvestDuty) start(ctx context.Context, l *reconcile.Lease, instance, bench string, sprints []string) {
	d.mu.Lock()
	if d.busy[bench] {
		d.mu.Unlock()
		return
	}
	if d.ctx == nil {
		d.ctx, d.cancel = context.WithCancel(ctx)
		d.held = map[string]string{}
	}
	if d.ctx.Err() != nil {
		d.mu.Unlock()
		return
	}
	wctx := d.ctx
	d.inst = instance
	d.busy[bench] = true
	d.wg.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.busy, bench)
			d.mu.Unlock()
		}()
		for _, s := range sprints {
			if wctx.Err() != nil || l.Fenced() {
				return
			}
			harvest.Run(wctx, d.st, harvest.Options{
				Sprint: s, Benches: []string{bench}, Instance: instance, Actor: "reconciler",
				Forge: d.forge, Pusher: d.pusher,
				Bound: l, Margin: reconcile.DefaultWriteMargin,
				OnLease: d.onLease, Log: d.log,
			})
		}
	}()
}

func (d *harvestDuty) onLease(bench, token string, held bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if held {
		d.held[bench] = token
	} else if d.held[bench] == token {
		delete(d.held, bench)
	}
}

// Stop cancels every harvest pass in flight and waits for them until ctx
// ends; each records its pass line and gives back its bench lease on the
// way. A lease still held after the wait is released by its token. It
// returns the benches released that way.
func (d *harvestDuty) Stop(ctx context.Context) []string {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
	waited := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
	}
	d.mu.Lock()
	held := make(map[string]string, len(d.held))
	for b, tok := range d.held {
		held[b] = tok
	}
	inst := d.inst
	d.mu.Unlock()
	var released []string
	for b, tok := range held {
		if r, err := harvest.Release(context.WithoutCancel(ctx), d.st, b, inst, tok); err == nil && r == "RELEASED" {
			released = append(released, b)
		}
	}
	sort.Strings(released)
	return released
}

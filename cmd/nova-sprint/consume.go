// The consume verb and the consumer duties (nova-tools #3323): each built
// consumer of #2756 4.5 runs under its own verb
//
//	nova-sprint consume <group> once|run --redis <addr> [--sprint <S>] [--bench <b>[,<b>...]] [--as <id>] [--every 1s]
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
// on the next pass. hold-to-fix (#3092, #3799) runs in the same pass under
// the same lease:route:<S>, after pr-to-read, once s:<S>:policy has fix_to
// and release_reader; `consume hold-to-fix once --sprint <S>` is one pass by
// hand.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Consumer groups, in `consume list` order.
const (
	groupPRToRead  = "pr-to-read"
	groupHoldToFix = "hold-to-fix"
)

// consumePRReadRemote is the remote seam for pr-to-read, swapped in tests.
var consumePRReadRemote consume.Remote = consume.GitRemote

func init() {
	register(Verb{
		Name:    "consume",
		Summary: "run one consumer (ok-to-friend, pr-to-read, hold-to-fix): consume <group> once|run --redis <addr>, or consume list",
		Run:     runConsume,
	})
	registerReconcileDuty(consume.GroupOkFriend, func(st *store.Store) (reconcileDuty, error) {
		return &okFriendDuty{st: st, started: map[string]bool{}}, nil
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
		return refuse(errOut, "consume", "wants a group (ok-to-friend, pr-to-read, hold-to-fix) and once|run, or list")
	}
	group := args[0]
	if group == "list" {
		fmt.Fprintf(out, "%s duty=reconcile verb=consume proc=proc:%s\n", consume.GroupOkFriend, consume.GroupOkFriend)
		fmt.Fprintf(out, "%s duty=reconcile,route verb=consume-once proc=proc:%s\n", groupPRToRead, groupPRToRead)
		fmt.Fprintf(out, "%s duty=reconcile,route verb=consume-once proc=proc:%s needs=fix_to,release_reader\n", groupHoldToFix, groupHoldToFix)
		return 0
	}
	if group != consume.GroupOkFriend && group != groupPRToRead && group != groupHoldToFix {
		return refuse(errOut, "consume", "unknown group "+group+"; want ok-to-friend, pr-to-read or hold-to-fix")
	}
	if len(args) < 2 || (args[1] != "once" && args[1] != "run") {
		return refuse(errOut, "consume "+group, "wants once or run")
	}
	mode := args[1]
	fs := taskFlags("consume " + group)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	consumer := fs.String("as", "", verbflag.HelpAs)
	every := fs.Duration("every", time.Second, "how often a run pass ticks")
	actor := &group
	if err := fs.Parse(args[2:]); err != nil {
		return refuse(errOut, "consume "+group, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "consume "+group, "takes flags after once|run, not positional arguments")
	}
	if *every <= 0 {
		return refuse(errOut, "consume "+group, "--every must be above zero")
	}
	if group == groupPRToRead || group == groupHoldToFix {
		if *sprint == "" {
			return refuse(errOut, "consume "+group, "--sprint is required")
		}
		if mode == "run" {
			return refuse(errOut, "consume "+group+" run", group+" runs under nova-sprint route --sprint <S>; consume "+group+" takes once")
		}
	}
	if *consumer == "" {
		host, _ := os.Hostname()
		*consumer = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	st, err := openReached(ctx, *redisAddr)
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
	if group == groupHoldToFix {
		h := &consume.HoldRoute{Store: st, Sprint: *sprint, Actor: *actor, Out: out}
		n, err := h.Once(ctx)
		var held *consume.LeaseHeldError
		switch {
		case errors.As(err, &held):
			fmt.Fprintf(errOut, "REFUSED hold-to-fix sprint=%s %s held by %s at %s\n", *sprint, consume.LeaseKey(*sprint), held.Holder, held.At)
			return 1
		case err != nil:
			fmt.Fprintf(errOut, "nova-sprint consume %s: %v\n", group, err)
			return 1
		}
		fmt.Fprintf(out, "CONSUMED hold-to-fix sprint=%s n=%d\n", *sprint, n)
		return 0
	}

	var pass func(ctx context.Context, quiet bool) int
	switch group {
	case consume.GroupOkFriend:
		started := map[string]bool{}
		pass = func(ctx context.Context, quiet bool) int {
			return okFriendPass(ctx, st, *sprint, *consumer, *actor, started, out, quiet)
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

// prReadDuty is pr-to-read and hold-to-fix as a reconcile duty: every pass,
// every open sprint in `sprints` (read again each pass, so a sprint opened
// after the reconciler started is joined on the next pass) gets one
// pr-to-read pass and then one hold-to-fix pass (#3799) under its own
// lease:route:<S> (consume.PRReadSprints with Hold), as consumer
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
			Remote: consumePRReadRemote, Out: d.out, Hold: true}
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

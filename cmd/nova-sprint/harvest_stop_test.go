package main

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// stuckPusher is a bench push that never answers: it tells the test it has
// started, then waits for its context (a push the owner can cancel) or, when
// deaf, for the test alone (a worker the owner cannot wait for).
type stuckPusher struct {
	started chan struct{}
	deaf    bool
	free    chan struct{}
}

func (p *stuckPusher) Push(ctx context.Context, _ harvest.BenchInfo, _ harvest.Card) error {
	select {
	case p.started <- struct{}{}:
	default:
	}
	if p.deaf {
		<-p.free
		return context.Canceled
	}
	<-ctx.Done()
	return ctx.Err()
}

// harvestStopFixture is one open sprint with one ended(DONE) card on one UP
// bench, and a reconciler lease over it.
func harvestStopFixture(t *testing.T) (*redis.Client, *store.Store, *reconcile.Lease, string) {
	t.Helper()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S, bench, label = "control-3737c0de", "ctl-b1", "card-1"
	identity := S + "/" + label + "/09fbedc9/" + bench + "/1"
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench+".fixture", "user", "nova", "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	pipe.HSet(ctx, "s:"+S+":card:"+label,
		"kind", "model", "repo", "nova-tools", "base", "dev", "base_sha", "09fbedc9", "state", "ended",
		"attempt", "1", "identity", identity, "bench", bench, "token_sha", "abcdefabcdef",
		"outcome", "DONE", "reason", "done", "pushed_sha", strings.Repeat("a", 40), "results", identity)
	pipe.SAdd(ctx, "s:"+S+":idx:card:ended", label)
	pipe.SAdd(ctx, "s:"+S+":bench:"+bench+":ended", label)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(c)
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Instance: "ctl-3737"})
	if err != nil {
		t.Fatal(err)
	}
	return c, st, l, bench
}

// TestHarvestDutyStopOnFence is nova-tools #3737: a reconciler that exits
// FENCED stops its harvest workers first. A worker mid-push is cancelled,
// records err=FENCED on proc:harvest:<b> and gives back lease:harvest:<b>; a
// worker that does not answer in time has its lease released by its token.
// Either way no bench is left held by the dead instance.
func TestHarvestDutyStopOnFence(t *testing.T) {
	ctx := context.Background()
	forge := &consumeForge{prs: map[string]harvest.PR{}}

	t.Run("worker records FENCED", func(t *testing.T) {
		c, st, l, bench := harvestStopFixture(t)
		pusher := &stuckPusher{started: make(chan struct{}, 1)}
		d := &harvestDuty{st: st, forge: forge, pusher: pusher, busy: map[string]bool{}}
		if _, err := d.Run(ctx, l); err != nil {
			t.Fatal(err)
		}
		waitStarted(t, pusher.started)
		if c.Exists(ctx, "lease:harvest:"+bench).Val() != 1 {
			t.Fatal("the worker holds no lease:harvest mid-push")
		}
		l.Fence(reconcile.ErrFenced) // a duty's fenced write was refused
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if got := d.Stop(sctx); len(got) != 0 {
			t.Fatalf("released by token %v, want none: the worker recorded its own pass", got)
		}
		h := c.HGetAll(ctx, "proc:harvest:"+bench).Val()
		if h["err"] != "FENCED" || h["n"] != "0" || h["left"] == "" || h["holder"] != "" {
			t.Fatalf("proc:harvest:%s = %v; want err=FENCED n=0, left recorded, holder cleared", bench, h)
		}
		if c.Exists(ctx, "lease:harvest:"+bench).Val() != 0 {
			t.Fatal("lease:harvest still held after the stop")
		}
	})

	t.Run("deaf worker released by token", func(t *testing.T) {
		c, st, l, bench := harvestStopFixture(t)
		pusher := &stuckPusher{started: make(chan struct{}, 1), deaf: true, free: make(chan struct{})}
		d := &harvestDuty{st: st, forge: forge, pusher: pusher, busy: map[string]bool{}}
		if _, err := d.Run(ctx, l); err != nil {
			t.Fatal(err)
		}
		waitStarted(t, pusher.started)
		l.Fence(reconcile.ErrFenced)
		// The owner's wait is already over: nothing more is waited for.
		sctx, cancel := context.WithCancel(ctx)
		cancel()
		if got := d.Stop(sctx); len(got) != 1 || got[0] != bench {
			t.Fatalf("released by token %v, want [%s]", got, bench)
		}
		if c.Exists(ctx, "lease:harvest:"+bench).Val() != 0 || c.HGet(ctx, "proc:harvest:"+bench, "holder").Val() != "" {
			t.Fatal("lease:harvest still held, or its holder still named, after the release by token")
		}
		close(pusher.free)
		d.wg.Wait()
	})
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the harvest worker never started its push")
	}
}

// fenceDuty deletes lease:reconciler on its second pass, as another instance
// taking over would leave it for this one.
type fenceDuty struct {
	c *redis.Client
	n atomic.Int32
}

func (d *fenceDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	if d.n.Add(1) == 2 {
		d.c.Del(ctx, reconcile.LeaseKey)
	}
	return reconcile.Counts{}, nil
}

// stopDuty records that the verb stopped it on the way out.
type stopDuty struct{ stopped atomic.Bool }

func (d *stopDuty) Run(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
	return reconcile.Counts{}, nil
}

func (d *stopDuty) Stop(context.Context) []string { d.stopped.Store(true); return []string{"ctl-b9"} }

// TestReconcileFencedStopsDuties (#3737): the verb that loses its lease exits
// 3 with the FENCED line, and stops every stoppable duty first, printing the
// bench leases released by token.
func TestReconcileFencedStopsDuties(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	seams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return &verbSSH{}, verbForge{} }
	registered := reconcileDuties
	t.Cleanup(func() { reconcileSeams, reconcileDuties = seams, registered })
	reconcileDuties = nil
	sd := &stopDuty{}
	registerReconcileDuty("ctl-fence", func(*store.Store) (reconcileDuty, error) { return &fenceDuty{c: c}, nil })
	registerReconcileDuty("ctl-stop", func(*store.Store) (reconcileDuty, error) { return sd, nil })

	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runReconcile(ctx, []string{"--redis", addr, "--host", "ctl-host"}, &out, &errOut) }()
	select {
	case code := <-done:
		if code != 3 {
			t.Fatalf("exit %d, want 3; out %q err %q", code, out.String(), errOut.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the verb did not exit after its lease was taken")
	}
	if !sd.stopped.Load() {
		t.Fatal("the stoppable duty was not stopped on the way out")
	}
	if !strings.Contains(out.String(), "HARVEST RELEASED bench=ctl-b9") || !strings.Contains(out.String(), "FENCED reconcile") {
		t.Fatalf("stdout %q, want the release line and the FENCED line", out.String())
	}
}

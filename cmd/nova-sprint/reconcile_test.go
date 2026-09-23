package main

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// verbSSH is the fixture benches' sshd for the verb: every session succeeds
// and keeps the `card launch --stdin` lines it carried (CI-NET: no host).
type verbSSH struct {
	mu    sync.Mutex
	lines []string
}

func (d *verbSSH) Dial(deal.Bench) deal.Session { return d }

func (d *verbSSH) Run(_ context.Context, stdin []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, l := range strings.Split(strings.TrimSpace(string(stdin)), "\n") {
		if l != "" {
			d.lines = append(d.lines, l)
		}
	}
	return nil
}

func (d *verbSSH) launched() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.lines)
}

// verbForge answers no PR: the fixture cards have no DEPENDS-ON.
type verbForge struct{}

func (verbForge) Ref(context.Context, string, int) (deal.Ref, error) {
	return deal.Ref{}, fmt.Errorf("fixture forge: no PRs")
}

// countingDuty stands in for a registered duty (the width tick of #3071).
type countingDuty struct{ n atomic.Int32 }

func (d *countingDuty) Run(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
	d.n.Add(1)
	return reconcile.Counts{}, nil
}

// syncBuffer is a bytes.Buffer the verb's goroutine and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestReconcileVerbDealsOnEvent (nova-tools #2935, Stella's hold at
// 30435ad2): the production `nova-sprint reconcile` builds its loop with the
// refill duty and the registered duties, so a fixture event is dealt by the
// running verb with no manual deal call. The verb's first pass deals the
// fixture bench full (restart); one child then ends, writing its slot-freed
// event, and the running verb refills the slot. Idle passes before the event
// deal nothing, and the refill lands inside half the 10 s sweep floor, so the
// event, not the sweep, woke it.
func TestReconcileVerbDealsOnEvent(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	const bench, S, slots, cards = "ctl-verb", "control-29350c0d", 4, 6

	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":desired", "slots", strconv.Itoa(slots))
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1")
	pipe.SAdd(ctx, "sprints", S)
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", "share", "1", "backpressure_missing", "open")
	for i := 0; i < cards; i++ {
		label := fmt.Sprintf("card-%02d", i)
		pipe.HSet(ctx, "s:"+S+":card:"+label, "state", "queued", "priority", strconv.Itoa(i),
			"attempt", "0", "retries", "0", "base_sha", "0123456789abcdef", "bench", "", "leg", "", "tier", "")
		pipe.ZAdd(ctx, "s:"+S+":pool", redis.Z{Score: float64(i), Member: label})
		pipe.SAdd(ctx, "s:"+S+":idx:card:queued", label)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	ssh := &verbSSH{}
	seams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return ssh, verbForge{} }
	registered := reconcileDuties
	// This control is the refill and the registration seam, so it runs with
	// only its own two duties; the consumer duties (#3323) have their own
	// control, TestConsumerVerbsRunOnce.
	reconcileDuties = nil
	extra := &countingDuty{}
	registerReconcileDuty("ctl-extra", func(*store.Store) (reconcileDuty, error) { return extra, nil })
	registerReconcileDuty("ctl-off", func(*store.Store) (reconcileDuty, error) { return nil, nil })
	t.Cleanup(func() { reconcileSeams, reconcileDuties = seams, registered })

	leased := func() int {
		n, err := c.ZCard(ctx, "bench:"+bench+":starting").Result()
		if err != nil {
			t.Fatal(err)
		}
		return int(n)
	}
	open := func() int {
		n, err := c.ZCard(ctx, "s:"+S+":pool").Result()
		if err != nil {
			t.Fatal(err)
		}
		return int(n)
	}
	waitFor := func(what string, within time.Duration, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(within)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: not within %s (working %d, open %d, launched %d)", what, within, leased(), open(), ssh.launched())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var out, errOut syncBuffer
	code := make(chan int, 1)
	go func() {
		code <- runReconcile(runCtx, []string{"--redis", addr, "--host", "ctl-host"}, &out, &errOut)
	}()

	// The verb's first pass (a new instance) deals the bench full.
	waitFor("restart deal", 5*time.Second, func() bool { return leased() == slots && ssh.launched() == slots })
	if got := open(); got != cards-slots {
		t.Fatalf("after the restart deal: open %d, want %d", got, cards-slots)
	}
	// Idle passes (the registered duty counts them) deal nothing: with no
	// event and the sweep not due, the running verb leaves the pool alone.
	passes := extra.n.Load()
	waitFor("two idle passes", 5*time.Second, func() bool { return extra.n.Load() >= passes+2 })
	if got := ssh.launched(); got != slots {
		t.Fatalf("idle passes launched %d, want %d: a deal ran with no event", got, slots)
	}

	// One child ends: the fixture event on cap:log and s:<S>:log. The running
	// verb refills the slot, far inside the 10 s sweep floor.
	members, err := c.ZRange(ctx, "bench:"+bench+":starting", 0, 0).Result()
	if err != nil || len(members) != 1 {
		t.Fatalf("starting: %v %v", members, err)
	}
	parts := strings.Split(members[0], "/")
	pipe = c.TxPipeline()
	pipe.ZRem(ctx, "bench:"+bench+":starting", members[0])
	pipe.HSet(ctx, "s:"+S+":card:"+parts[1], "state", "done", "outcome", "DONE")
	pipe.SMove(ctx, "s:"+S+":idx:card:dealt", "s:"+S+":idx:card:done", parts[1])
	pipe.ZRem(ctx, "s:"+S+":bench:"+bench+":queue", parts[1])
	pipe.SAdd(ctx, "s:"+S+":bench:"+bench+":ended", parts[1])
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + S + ":log", Values: []string{
		"kind", "card end", "id", parts[1], "from", "dealt", "to", "done", "attempt", parts[2], "actor", "ctl-card"}})
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "cap:log", Values: []string{
		"kind", "slot-freed", "target", "bench:" + bench, "slots", "1", "actor", "ctl-card"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// The bound is below the 10 s sweep floor: only the event can deal here.
	waitFor("refill on the slot-freed event", reconcile.DefaultSweep/2, func() bool {
		return leased() == slots && open() == cards-slots-1 && ssh.launched() == slots+1
	})

	stop()
	select {
	case got := <-code:
		if got != 0 {
			t.Fatalf("reconcile exit %d, want 0; stderr %s", got, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reconcile did not stop on cancel")
	}
	if !strings.Contains(out.String(), "DUTIES refill,ctl-extra\n") {
		t.Fatalf("stdout %q, want the duty line `DUTIES refill,ctl-extra` (ctl-off is switched off)", out.String())
	}
	if !strings.Contains(out.String(), "RELEASED reconcile") {
		t.Fatalf("stdout %q, want the lease released", out.String())
	}
	if extra.n.Load() < 2 {
		t.Fatalf("registered duty ran %d passes, want one per pass", extra.n.Load())
	}
	if strings.Contains(errOut.String(), "pass:") {
		t.Fatalf("a pass failed: %s", errOut.String())
	}
}

// failingDuty stands in for a duty whose pass fails (the deal pass of #3321).
type failingDuty struct{}

func (failingDuty) Run(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
	return reconcile.Counts{}, fmt.Errorf("deal: reserve control-00003321: ns_card_deal: boom")
}

// TestReconcileOnceSurfacesDutyErrors (nova-tools #3321): a duty error used
// to reach only proc:reconciler err while `reconcile --once` printed RELEASED
// and exited 0. Now --once still releases the lease, then prints the duty's
// name and error and exits 1; a clean --once pass still exits 0.
func TestReconcileOnceSurfacesDutyErrors(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	seams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return &verbSSH{}, verbForge{} }
	registered := reconcileDuties
	t.Cleanup(func() { reconcileSeams, reconcileDuties = seams, registered })

	extra := &countingDuty{}
	registerReconcileDuty("ctl-extra", func(*store.Store) (reconcileDuty, error) { return extra, nil })
	var out, errOut bytes.Buffer
	if got := runReconcile(ctx, []string{"--redis", addr, "--host", "ctl-host", "--once"}, &out, &errOut); got != 0 {
		t.Fatalf("clean --once exit %d, want 0; stdout %q stderr %q", got, out.String(), errOut.String())
	}
	if extra.n.Load() != 1 || errOut.Len() != 0 {
		t.Fatalf("clean --once: duty ran %d passes, stderr %q; want 1 and empty", extra.n.Load(), errOut.String())
	}

	registerReconcileDuty("ctl-broken", func(*store.Store) (reconcileDuty, error) { return failingDuty{}, nil })
	out.Reset()
	errOut.Reset()
	got := runReconcile(ctx, []string{"--redis", addr, "--host", "ctl-host", "--once"}, &out, &errOut)
	if got != 1 {
		t.Fatalf("--once with a failing duty exit %d, want 1; stdout %q stderr %q", got, out.String(), errOut.String())
	}
	want := "duty ctl-broken: deal: reserve control-00003321: ns_card_deal: boom"
	if !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr %q, want the duty name and error %q", errOut.String(), want)
	}
	if !strings.Contains(out.String(), "RELEASED reconcile") {
		t.Fatalf("stdout %q, want the lease released before the failure exit", out.String())
	}
	if n, err := c.Exists(ctx, "lease:reconciler").Result(); err != nil || n != 0 {
		t.Fatalf("lease:reconciler exists=%d err=%v after --once, want released", n, err)
	}
}

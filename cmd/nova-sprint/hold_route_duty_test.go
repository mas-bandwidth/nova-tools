package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// holdDutyPass runs one pass of the reconciler's pr-to-read duty (the one
// productionDuties registers under lease:route:<S>) and waits for it.
func holdDutyPass(t *testing.T, d *prReadDuty, l *reconcile.Lease) {
	t.Helper()
	if _, err := d.Run(context.Background(), l); err != nil {
		t.Fatalf("duty run: %v", err)
	}
	d.wg.Wait()
	d.mu.Lock()
	err := d.lastErr
	d.mu.Unlock()
	if err != nil {
		t.Fatalf("duty pass: %v", err)
	}
}

// TestReconcileRoutesHoldToFix is nova-tools #3799: the reconciler's route
// duty runs the #3092 hold router pass under lease:route:<S> once the
// policy has fix_to and release_reader. A typed HOLD line ingested by `hold
// ingest` makes no fix task while the policy lacks them (one HOLDROUTE WAIT
// line) or while another instance holds lease:route:<S>; with the policy and
// the lease free, one pass makes exactly one fix task,
// fix-<n>-hold-<holder>-<sha8>, on fix_to's queue, writes proc:hold-to-fix
// and gives the lease back. The reconcile route duty's own fix leg then
// leaves that sprint's HOLD to the hold router (fixes=0, no second fix task).
func TestReconcileRoutesHoldToFix(t *testing.T) {
	const S, pr = "control-3799h", 37
	ctx, c, addr, _ := routeFixture(t, S)
	e := &holdEnv{t: t, c: c, addr: addr, S: S}
	e.friends("stella", "johnny", "rowan")
	e.unit(pr, headA, "johnny")
	e.mustIngest(pr, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")

	remote := consumePRReadRemote
	consumePRReadRemote = func(context.Context, string) (map[int]string, error) { return map[int]string{}, nil }
	t.Cleanup(func() { consumePRReadRemote = remote })

	st := store.New(c)
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Instance: "ctl-3799"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d := &prReadDuty{st: st, out: &out}
	fix := disposition.FixID("37", "stella", headA)

	// No fix_to/release_reader: the pass routes nothing and says why.
	holdDutyPass(t, d, l)
	if ids := e.fixIDs(); len(ids) != 0 {
		t.Fatalf("fix tasks without the policy: %v", ids)
	}
	if !strings.Contains(out.String(), "HOLDROUTE WAIT sprint="+S+" no fix_to/release_reader") {
		t.Fatalf("no HOLDROUTE WAIT line in %q", out.String())
	}
	if groups, _ := c.XInfoGroups(ctx, disposition.EventsKey(S)).Result(); len(groups) != 0 {
		t.Fatalf("the hold router read the events with no policy: groups %v", groups)
	}

	// The policy set, but another router holds lease:route:<S>: skipped.
	e.policy("rowan", "stella")
	if r, err := c.FCall(ctx, consume.FunctionRouteLeaseTake, nil, S, "other", "tok-other", "elsewhere", "6000").StringSlice(); err != nil || len(r) == 0 || r[0] == "HELD" {
		t.Fatalf("take lease as other: %v %v", r, err)
	}
	holdDutyPass(t, d, l)
	if ids := e.fixIDs(); len(ids) != 0 {
		t.Fatalf("fix tasks while lease:route:%s is held elsewhere: %v", S, ids)
	}
	if err := c.FCall(ctx, consume.FunctionRouteLeaseRelease, nil, S, "other", "tok-other").Err(); err != nil {
		t.Fatal(err)
	}

	// The lease free: exactly one fix task on fix_to's queue.
	out.Reset()
	holdDutyPass(t, d, l)
	if ids := e.fixIDs(); len(ids) != 1 || ids[0] != fix {
		t.Fatalf("fix tasks %v, want [%s]\nout: %s", ids, fix, out.String())
	}
	if q := e.queue("rowan"); len(q) != 1 || q[0] != fix {
		t.Fatalf("fix_to queue %v, want [%s]", q, fix)
	}
	if !strings.Contains(out.String(), "HOLDROUTE event ") {
		t.Fatalf("no HOLDROUTE event line in %q", out.String())
	}
	if c.Exists(ctx, "proc:"+consume.GroupHoldToFix).Val() != 1 {
		t.Fatal("no proc:hold-to-fix")
	}
	if c.Exists(ctx, consume.LeaseKey(S)).Val() != 0 {
		t.Fatalf("%s still held after the pass", consume.LeaseKey(S))
	}
	// A further pass makes nothing more.
	holdDutyPass(t, d, l)
	if ids := e.fixIDs(); len(ids) != 1 {
		t.Fatalf("fix tasks after a second pass: %v", ids)
	}
	if err := l.Release(ctx); err != nil {
		t.Fatal(err)
	}

	// The reconcile route duty leaves the hold router's HOLD alone.
	o := reconcileOnce(t, addr)
	if line := dutyLine(t, o, "route"); !strings.Contains(line, "fixes=0") {
		t.Fatalf("route duty receipt %q, want fixes=0", line)
	}
	if n := c.Exists(ctx, "task:fix-37-"+headA[:8]).Val(); n != 0 {
		t.Fatalf("the route duty made a second fix task for the same HOLD")
	}
	if ids := e.fixIDs(); len(ids) != 1 {
		t.Fatalf("fix tasks after reconcile: %v", ids)
	}
}

// TestReconcileHoldSingleFixTask is nova-tools #3833: a sprint with
// fix_to/release_reader and a HOLD at head present both as a pr:: line and as a
// hold event gets exactly one fix task after reconcile --once (counting
// task:fix-* and s::task:fix-*).
func TestReconcileHoldSingleFixTask(t *testing.T) {
	const S, pr = "control-3833s", 39
	ctx, c, addr, _ := routeFixture(t, S)
	e := &holdEnv{t: t, c: c, addr: addr, S: S}
	e.friends("stella", "johnny", "rowan")
	e.policy("rowan", "stella")
	e.unit(pr, headA, "johnny")
	e.mustIngest(pr, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")

	c.HSet(ctx, "task:build-39", "stream", "nova-sprint", "state", "working", "owner", "johnny",
		"pr", "mas-bandwidth/nova-tools#39", "kind", "build", "branch", "codex/39-anything")
	c.ZAdd(ctx, "ws:nova-sprint:working", redis.Z{Score: 1, Member: "build-39"})
	c.HSet(ctx, prkey.Key("mas-bandwidth/nova-tools", pr), "repo", "mas-bandwidth/nova-tools", "n", "39",
		"head", headA, "base", "dev", "stream", "nova-sprint", "task", "build-39", "state", "open", "ci", "green",
		"reads", typed("stella", headA, "HOLD", 5, substance))

	remote := consumePRReadRemote
	consumePRReadRemote = func(context.Context, string) (map[int]string, error) { return map[int]string{}, nil }
	t.Cleanup(func() { consumePRReadRemote = remote })

	o := reconcileOnce(t, addr)
	if line := dutyLine(t, o, "route"); !strings.Contains(line, "fixes=0") {
		t.Fatalf("route duty receipt %q, want fixes=0", line)
	}
	taskFixKeys, err := c.Keys(ctx, "task:fix-*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sprintFixKeys, err := c.Keys(ctx, "s:"+S+":task:fix-*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if total := len(taskFixKeys) + len(sprintFixKeys); total != 1 {
		t.Fatalf("fix tasks: task:fix-*=%v, s::task:fix-*=%v, want exactly one", taskFixKeys, sprintFixKeys)
	}
	if len(taskFixKeys) != 0 {
		t.Fatalf("the route duty made a second fix task for the same HOLD: %v", taskFixKeys)
	}
	fix := disposition.FixID("39", "stella", headA)
	if len(sprintFixKeys) != 1 || sprintFixKeys[0] != "s:"+S+":task:"+fix {
		t.Fatalf("sprint fix tasks: %v, want [%s]", sprintFixKeys, "s:"+S+":task:"+fix)
	}
	if q := e.queue("rowan"); len(q) != 1 || q[0] != fix {
		t.Fatalf("fix_to queue %v, want [%s]", q, fix)
	}
}

// TestRouteVerbRunsHoldToFix is nova-tools #3799 for `nova-sprint route`:
// the start line names hold-to-fix among the rules and classify alone as not
// wired, and the running router turns a HOLD into its fix task.
func TestRouteVerbRunsHoldToFix(t *testing.T) {
	const S, pr = "control-3799r", 38
	_, c, addr, _ := routeFixture(t, S)
	e := &holdEnv{t: t, c: c, addr: addr, S: S}
	e.friends("stella", "johnny", "rowan")
	e.policy("rowan", "stella")
	e.unit(pr, headA, "johnny")
	e.mustIngest(pr, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	fix := disposition.FixID("38", "stella", headA)

	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runRoute(ctx, []string{"--redis", addr, "--sprint", S}, &out, &errOut) }()
	deadline := time.Now().Add(holdWait())
	for time.Now().Before(deadline) && c.Exists(context.Background(), "s:"+S+":task:"+fix).Val() == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	code := <-done
	if code != 0 {
		t.Fatalf("route: exit %d err %q", code, errOut.String())
	}
	start := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(start, ",hold-to-fix") || !strings.HasSuffix(start, "not-wired=classify(#3076)") {
		t.Fatalf("route start line %q", start)
	}
	if ids := e.fixIDs(); len(ids) != 1 || ids[0] != fix {
		t.Fatalf("fix tasks %v, want [%s]\nout: %s", ids, fix, out.String())
	}
}

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
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

//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
	reconcileSeams = func(*store.Store) (deal.Dialer, deal.PRs) { return &verbSSH{}, verbForge{} }
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
	// #3620: every loop line carries its UTC time first.
	stamped := regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z nova-sprint reconcile: duty ctl-broken: `)
	if !stamped.MatchString(errOut.String()) {
		t.Fatalf("stderr %q, want the duty line led by its UTC timestamp", errOut.String())
	}
	if !strings.Contains(out.String(), "RELEASED reconcile") {
		t.Fatalf("stdout %q, want the lease released before the failure exit", out.String())
	}
	if n, err := c.Exists(ctx, "lease:reconciler").Result(); err != nil || n != 0 {
		t.Fatalf("lease:reconciler exists=%d err=%v after --once, want released", n, err)
	}
}

//go:build functional

package deal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// noSessionLauncher starts nothing: the control is about which cards the pass
// reserves, not how they reach the bench.
type noSessionLauncher struct{}

func (noSessionLauncher) Launch(context.Context, Opener, Bench, []Reservation) error { return nil }

type nopDialer struct{}

func (nopDialer) Dial(Bench) Session { return nil }

// TestDependentDealtOnlyAfterDependencyPRMerged is #3066's DONE-WHEN on the
// real nova_sprint functions: task B depends on task A whose PR is open, so B
// is not dealt (it moves to s:<S>:waiting and the pool the table counts drops
// it) while C, which waits for nothing, is; merge A's PR and B is dealt in the
// next pass.
func TestDependentDealtOnlyAfterDependencyPRMerged(t *testing.T) {
	t.Parallel()

	const sprint = "control-00003066"
	ctx := context.Background()
	c := dealRedis(t)
	seedFleet(t, c, sprint, 0, map[string]int{"ctl-a": 64})
	seedLease(t, c, "live-token")
	ck := func(label string) string { return "s:" + sprint + ":card:" + label }
	// A: built, its PR open and under review; not in the pool.
	c.HSet(ctx, ck("task-a"), "state", "review-ready", "repo", "ctl/repo", "base", "dev", "pr", "42", "attempt", "1")
	// B depends on A; C depends on nothing.
	for i, label := range []string{"task-b", "task-c"} {
		deps := "task-a"
		if label == "task-c" {
			deps = "-"
		}
		c.HSet(ctx, ck(label), "state", "queued", "priority", fmt.Sprint(i), "attempt", "0", "retries", "0",
			"base_sha", "0123456789abcdef", "repo", "ctl/repo", "base", "dev", "depends_on", deps, "bench", "", "leg", "", "tier", "")
		c.ZAdd(ctx, "s:"+sprint+":pool", redis.Z{Score: float64(i), Member: label})
		c.SAdd(ctx, "s:"+sprint+":idx:card:queued", label)
	}
	prs := newFakePRs()
	prs.set("ctl/repo#42", Ref{IsPR: true, State: "open", Base: "dev"})
	st := newFnStore(c)
	p := &Pass{Source: RedisSource{Client: c}, Fence: fence("live-token"), Reserver: st, Row: st, Gate: st,
		PRs: prs, Launcher: noSessionLauncher{}, Dialer: nopDialer{}}

	dealt := func(res Result) []string {
		var out []string
		for _, b := range res.Benches {
			for _, r := range b.Dealt {
				out = append(out, r.Card.Label)
			}
		}
		return out
	}

	// Pass 1: A's PR is open.
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dealt(res), ","); got != "task-c" {
		t.Fatalf("pass 1 dealt %q, want only task-c: task-b depends on task-a whose PR #42 is open", got)
	}
	if st := card(t, c, sprint, "task-b"); st["state"] != "queued" || !strings.Contains(st["wait_why"], "task-a (ctl/repo#42) open") {
		t.Fatalf("task-b after pass 1: %v; want queued with wait_why naming task-a's open PR", st)
	}
	if ok, _ := c.SIsMember(ctx, "s:"+sprint+":waiting", "task-b").Result(); !ok {
		t.Fatal("task-b is not in s:<S>:waiting after pass 1")
	}
	if n := zcard(t, c, "s:"+sprint+":pool"); n != 0 {
		t.Fatalf("pool holds %d after pass 1, want 0: the ready antichain must not count task-b", n)
	}
	if len(res.Waiting) != 1 || res.Waiting[0].Label != "task-b" {
		t.Fatalf("pass 1 reported waiting %+v, want task-b", res.Waiting)
	}
	if n := len(logEntries(t, c, sprint, "card wait", "task-b")); n != 1 {
		t.Fatalf("%d card wait receipts for task-b, want 1", n)
	}

	// Pass 2, nothing changed: B stays waiting and nothing more is written.
	res, err = p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := dealt(res); len(got) != 0 {
		t.Fatalf("pass 2 dealt %v with task-a's PR still open", got)
	}
	if n := len(logEntries(t, c, sprint, "card wait", "task-b")); n != 1 {
		t.Fatalf("%d card wait receipts for task-b after a repeat pass, want still 1", n)
	}

	// Merge A's PR: B is dealt in the next pass.
	prs.set("ctl/repo#42", Ref{IsPR: true, Merged: true, State: "closed", Base: "dev"})
	res, err = p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dealt(res), ","); got != "task-b" {
		t.Fatalf("the pass after the merge dealt %q, want task-b", got)
	}
	if res.Released != 1 {
		t.Fatalf("released %d, want 1", res.Released)
	}
	if st := card(t, c, sprint, "task-b"); st["state"] != "dealt" || st["bench"] != "ctl-a" || st["wait_why"] != "" {
		t.Fatalf("task-b after the merge: %v; want dealt on ctl-a, wait_why cleared", st)
	}
	if n := scard(t, c, "s:"+sprint+":waiting"); n != 0 {
		t.Fatalf("waiting holds %d after the merge, want 0", n)
	}
	if n := len(logEntries(t, c, sprint, "card release", "task-b")); n != 1 {
		t.Fatalf("%d card release receipts for task-b, want 1", n)
	}
}

package deal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fakePRs is the forge in a map: <repo>#<n> -> Ref. A missing number is an
// error, as a forge that did not answer is. It counts the questions asked.
type fakePRs struct {
	mu    sync.Mutex
	refs  map[string]Ref
	asked map[string]int
}

func newFakePRs() *fakePRs { return &fakePRs{refs: map[string]Ref{}, asked: map[string]int{}} }

func (f *fakePRs) set(ref string, r Ref) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs[ref] = r
}

func (f *fakePRs) Ref(_ context.Context, repo string, n int) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := fmt.Sprintf("%s#%d", repo, n)
	f.asked[k]++
	r, ok := f.refs[k]
	if !ok {
		return Ref{}, errors.New("forge did not answer")
	}
	return r, nil
}

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

// TestReadyNamesEveryEntry holds the gate's reading of each DEPENDS-ON form:
// only a merge into the card's base, a closed issue, a landed card or a card
// closed with nothing to land releases it; everything else is named.
func TestReadyNamesEveryEntry(t *testing.T) {
	ctx := context.Background()
	prs := newFakePRs()
	prs.set("o/r#1", Ref{IsPR: true, Merged: true, State: "closed", Base: "dev"})
	prs.set("o/r#2", Ref{IsPR: true, Merged: true, State: "closed", Base: "main"})
	prs.set("o/r#3", Ref{IsPR: true, State: "closed", Base: "dev"})
	prs.set("o/r#4", Ref{State: "closed"})
	prs.set("o/r#5", Ref{State: "open"})
	prs.set("o/r#6", Ref{IsPR: true, State: "open", Base: "dev"})
	card := func(label string, deps ...string) Card {
		return Card{Sprint: "s", Label: label, Repo: "o/r", Base: "dev", DependsOn: deps}
	}
	in := Input{
		Now: time.Now(),
		Sprints: []Sprint{{Name: "s", Pool: []Card{
			card("none", "-"),
			card("empty"),
			card("pr-merged", "o/r#1"),
			card("pr-other-base", "o/r#2"),
			card("pr-closed", "o/r#3"),
			card("issue-closed", "o/r#4"),
			card("issue-open", "o/r#5"),
			card("unanswered", "o/r#99"),
			card("landed", "dep-landed"),
			card("no-pr-done", "dep-no-pr"),
			card("cancelled", "dep-cancelled"),
			card("missing", "dep-gone"),
			card("dep-pr-open", "dep-open"),
			card("two", "o/r#1", "o/r#6"),
		}, Waiting: []Card{
			{Sprint: "s", Label: "was-waiting", Repo: "o/r", Base: "dev", DependsOn: []string{"o/r#1"}, WaitWhy: "o/r#1 open"},
		}}},
		Deps: map[string]DepCard{
			"s/dep-landed":    {Found: true, State: "landed", PR: 7, Repo: "o/r"},
			"s/dep-no-pr":     {Found: true, State: "ended", Outcome: "DONE"},
			"s/dep-cancelled": {Found: true, State: "cancelled"},
			"s/dep-open":      {Found: true, State: "review-ready", PR: 6},
		},
	}
	out, moves, blocked := Ready(ctx, in, prs)
	var pool []string
	for _, c := range out.Sprints[0].Pool {
		pool = append(pool, c.Label)
	}
	if got := strings.Join(pool, ","); got != "none,empty,pr-merged,issue-closed,landed,no-pr-done,was-waiting" {
		t.Fatalf("ready pool %s", got)
	}
	why := map[string]string{}
	for _, b := range blocked {
		why[b.Label] = b.Why
	}
	for label, want := range map[string]string{
		"pr-other-base": "o/r#2 merged into main, not dev",
		"pr-closed":     "o/r#3 can no longer land: closed without merge",
		"issue-open":    "o/r#5 open",
		"unanswered":    "o/r#99 unknown: forge did not answer",
		"cancelled":     "dep-cancelled can no longer land: card cancelled without a PR",
		"missing":       "dep-gone can no longer land: no such card in sprint s",
		"dep-pr-open":   "dep-open (o/r#6) open",
		"two":           "o/r#6 open",
	} {
		if why[label] != want {
			t.Errorf("%s: why %q, want %q", label, why[label], want)
		}
	}
	if len(blocked) != 8 {
		t.Errorf("%d blocked, want 8: %+v", len(blocked), blocked)
	}
	var released int
	for _, m := range moves["s"] {
		if m.Verb == GateRelease {
			released++
			if m.Label != "was-waiting" {
				t.Errorf("released %s", m.Label)
			}
		}
	}
	if released != 1 {
		t.Errorf("released %d, want 1", released)
	}
	// One question per number per pass, however many cards name it.
	if n := prs.asked["o/r#1"]; n != 1 {
		t.Errorf("o/r#1 asked %d times in one pass, want 1", n)
	}
	// No forge seam: a card that waits on a PR is never dealt.
	if out, _, blocked := Ready(ctx, Input{Sprints: []Sprint{{Name: "s", Pool: []Card{card("x", "o/r#1")}}}}, nil); len(out.Sprints[0].Pool) != 0 || len(blocked) != 1 {
		t.Fatalf("with no forge seam the pool is %+v", out.Sprints[0].Pool)
	}
}

// TestGHRefReadsPullsThenIssues runs GH against a fake gh in the test's temp
// directory: a PR answers from pulls/<n>; a 404 there reads issues/<n>.
func TestGHRefReadsPullsThenIssues(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gh")
	script := `#!/bin/sh
case "$2" in
  repos/o/r/pulls/1) echo '{"merged":true,"state":"closed","base":"dev"}' ;;
  repos/o/r/pulls/2) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
  repos/o/r/issues/2) echo closed ;;
  *) echo 'gh: HTTP 502' >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := GH{Program: fake}
	ctx := context.Background()
	if r, err := g.Ref(ctx, "o/r", 1); err != nil || !r.IsPR || !r.Merged || r.Base != "dev" {
		t.Fatalf("pulls/1: %+v %v", r, err)
	}
	if r, err := g.Ref(ctx, "o/r", 2); err != nil || r.IsPR || r.State != "closed" {
		t.Fatalf("issue 2: %+v %v", r, err)
	}
	if _, err := g.Ref(ctx, "o/r", 3); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("a forge error must stay an error, got %v", err)
	}
}

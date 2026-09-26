//go:build functional

package reap_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reap"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// forge stands in for GitHub: it counts PATCH state=closed per PR, fails
// the test on any read, and answers each PR's closes from a script.
type forge struct {
	t      *testing.T
	mu     sync.Mutex
	closes map[int]int
	fail   map[int][]int // status codes to answer, in order, before a 200
	during func(n int)   // runs inside a close, before it answers
	srv    *httptest.Server
}

func newForge(t *testing.T) *forge {
	f := &forge{t: t, closes: map[int]int{}, fail: map[int][]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("forge read %s %s: the reap never reads GitHub", r.Method, r.URL.Path)
			http.Error(w, `{"message":"no reads"}`, http.StatusForbidden)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		n, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/repos/"+repo+"/pulls/"))
		if err != nil || body["state"] != "closed" {
			t.Errorf("forge: unexpected %s %s %v", r.Method, r.URL.Path, body)
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
			return
		}
		f.mu.Lock()
		f.closes[n]++
		var code int
		if q := f.fail[n]; len(q) > 0 {
			code, f.fail[n] = q[0], q[1:]
		}
		during := f.during
		f.mu.Unlock()
		if during != nil {
			during(n)
		}
		if code != 0 {
			http.Error(w, `{"message":"bad gateway"}`, code)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *forge) gh(budget int) *stream.GitHub {
	return &stream.GitHub{API: f.srv.URL, Token: "test-token", Budget: budget}
}

func (f *forge) closed() map[int]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]int{}
	for k, v := range f.closes {
		out[k] = v
	}
	return out
}

func store(t *testing.T) (context.Context, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	return ctx, c
}

// card seeds one card PR: the prcard entry, the card's origin and, unless
// rec is nil, the PR record.
func card(t *testing.T, ctx context.Context, c *redis.Client, n int, label string, issue int, rec map[string]string) {
	t.Helper()
	must(t, c.HSet(ctx, "s:"+S+":prcard", fmt.Sprintf("%s#%d", repo, n), label).Err())
	if issue > 0 {
		must(t, c.HSet(ctx, "s:"+S+":card:"+label, "origin", fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue)).Err())
	}
	if rec == nil {
		return
	}
	m := map[string]any{"head": head(n), "base": "dev", "stream": "landing", "label": label, "sprint": S, "state": "open"}
	for k, v := range rec {
		m[k] = v
	}
	must(t, c.HSet(ctx, fmt.Sprintf("pr:nova-tools:%d", n), m).Err())
}

// task seeds one sprint task naming ref (nova-tools#<n>) in the ref index.
func task(t *testing.T, ctx context.Context, c *redis.Client, id, state, kind string, ref int, pr string) {
	t.Helper()
	must(t, c.HSet(ctx, "task:"+id, "state", state, "kind", kind, "repo", repo, "pr", pr).Err())
	must(t, c.SAdd(ctx, fmt.Sprintf("ref:nova-tools#%d:tasks", ref), id).Err())
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func rec(t *testing.T, ctx context.Context, c *redis.Client, n int) map[string]string {
	t.Helper()
	m, err := c.HGetAll(ctx, fmt.Sprintf("pr:nova-tools:%d", n)).Result()
	must(t, err)
	return m
}

func score(n, s int, who string) string {
	return fmt.Sprintf("SCORE who=%s head=%s score=%d/10", who, head(n)[:8], s)
}

// fixture is the DONE-WHEN sprint: three PRs to reap, one per rule, and the
// PRs the reap must leave alone.
func fixture(t *testing.T, ctx context.Context, c *redis.Client) {
	// 101: its card's issue 50 landed by card PR 102.
	card(t, ctx, c, 101, "card-a", 50, map[string]string{})
	card(t, ctx, c, 102, "card-a2", 50, map[string]string{"state": "landed"})
	// 103: read 6 at head, the card's build task closed.
	card(t, ctx, c, 103, "card-b", 51, map[string]string{"reads": score(103, 6, "emma")})
	task(t, ctx, c, "build-51-b", "closed", "build", 51, "")
	// 104: branch gone; a read task in working is not work that keeps it.
	card(t, ctx, c, 104, "card-c", 53, map[string]string{"branch_gone": "fetch:404"})
	task(t, ctx, c, "read-104-44444444", "working", "read", 104, "104")
	// 105: superseded by landed 106, but an APPROVE at its head keeps it.
	card(t, ctx, c, 105, "card-d", 52, map[string]string{"reads": score(105, 5, "emma") + "\n" + score(105, 9, "stella")})
	card(t, ctx, c, 106, "card-d2", 52, map[string]string{"state": "merged"})
	// 107: branch gone, but a live fix task names it.
	card(t, ctx, c, 107, "card-e", 54, map[string]string{"branch_gone": "1"})
	task(t, ctx, c, "fix-107-e", "working", "fix", 107, "107")
	// 108: a card PR with no record.
	card(t, ctx, c, 108, "card-f", 55, nil)
	// 109: read 5 at an older head only; open, nothing else: kept.
	card(t, ctx, c, 109, "card-g", 56, map[string]string{"reads": "SCORE who=emma head=abcdef12 score=5/10"})
	task(t, ctx, c, "build-56-g", "closed", "build", 56, "")
}

// TestReapClosesStaleSupersededPRs is the DONE-WHEN of nova-tools#3156: on a
// sprint whose records name one PR landed by another, one read under 8 with
// its card closed and one whose branch is gone, pr reap closes exactly those
// three with one GitHub PATCH each and no GitHub read, writes the reason on
// each record (reap, reap_by, reap_close=done, state=closed) and leaves the
// PR with an APPROVE at head, the PR with a live fix, the unrecorded PR and
// the plain open PR untouched; a second run makes no call.
func TestReapClosesStaleSupersededPRs(t *testing.T) {
	t.Parallel()

	ctx, c := store(t)
	fixture(t, ctx, c)
	f := newForge(t)
	rep, err := reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	got := f.closed()
	if len(got) != 3 || got[101] != 1 || got[103] != 1 || got[104] != 1 {
		t.Fatalf("closes = %v, want 101, 103, 104 once each\n%s", got, strings.Join(rep.Lines, "\n"))
	}
	want := map[int][2]string{101: {reap.RuleLandedBy, "nova-tools#102"}, 103: {reap.RuleReadUnder8, "who=emma"}, 104: {reap.RuleBranchGone, "branch"}}
	for n, w := range want {
		m := rec(t, ctx, c, n)
		if m["reap"] != w[0] || m["reap_by"] != w[1] || m["reap_close"] != "done" || m["state"] != "closed" || m["closed_at"] == "" || m["reap_head"] != head(n) {
			t.Errorf("pr %d record = %v, want reap=%s by=%s done closed", n, m, w[0], w[1])
		}
	}
	for _, n := range []int{105, 107, 109} {
		m := rec(t, ctx, c, n)
		if m["reap"] != "" || m["state"] != "open" {
			t.Errorf("pr %d was touched: %v", n, m)
		}
	}
	if rep.PRs != 9 || rep.Closed != 3 || rep.Reap != 3 || rep.Keep != 3 || rep.Done != 2 || rep.Miss != 1 || rep.Calls != 3 {
		t.Errorf("report = %s", rep.Summary(S, false))
	}
	// The reason is logged once per decision and close.
	if n, _ := c.XLen(ctx, "s:"+S+":log").Result(); n != 6 {
		t.Errorf("s:%s:log has %d entries, want 6 (3 decided + 3 done)", S, n)
	}
	rep2, err := reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Calls != 0 || rep2.Reap != 0 || rep2.Done != 5 || len(f.closed()) != 3 {
		t.Errorf("second run: %s closes %v", rep2.Summary(S, false), f.closed())
	}
}

// TestReapDryRunWritesNothing: the same decisions, no write, no call.
func TestReapDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	ctx, c := store(t)
	fixture(t, ctx, c)
	f := newForge(t)
	rep, err := reap.Run(ctx, c, reap.Options{Sprint: S, DryRun: true, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	var reaped []string
	for _, l := range rep.Lines {
		if strings.HasPrefix(l, "REAP ") {
			reaped = append(reaped, strings.Fields(l)[1])
		}
	}
	sort.Strings(reaped)
	if strings.Join(reaped, " ") != repo+"#101 "+repo+"#103 "+repo+"#104" || len(f.closed()) != 0 {
		t.Fatalf("dry run reaped %v, closes %v", reaped, f.closed())
	}
	for _, n := range []int{101, 103, 104} {
		if m := rec(t, ctx, c, n); m["reap"] != "" || m["state"] != "open" {
			t.Errorf("dry run wrote pr %d: %v", n, m)
		}
	}
	if n, _ := c.Exists(ctx, "s:"+S+":log").Result(); n != 0 {
		t.Errorf("dry run logged")
	}
}

// TestReapBudgetDefersAndFailedCloseRetries: a budget of one closes the
// oldest PR and leaves the rest pending with their reason recorded; a 502 is
// failed:502 with one attempt and the next run closes it; ten failures are
// STUCK and make no call.
func TestReapBudgetDefersAndFailedCloseRetries(t *testing.T) {
	t.Parallel()

	ctx, c := store(t)
	fixture(t, ctx, c)
	f := newForge(t)
	f.fail[103] = []int{502}
	rep, err := reap.Run(ctx, c, reap.Options{Sprint: S, Budget: 1, Closer: f.gh(1)})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.closed(); len(got) != 1 || got[101] != 1 || rep.Deferred != 2 {
		t.Fatalf("budget 1: closes %v, %s", got, rep.Summary(S, false))
	}
	for _, n := range []int{103, 104} {
		if m := rec(t, ctx, c, n); m["reap_close"] != "pending" || m["reap"] == "" || m["state"] != "open" {
			t.Errorf("deferred pr %d = %v, want the reason recorded and close pending", n, m)
		}
	}
	rep, err = reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	if m := rec(t, ctx, c, 103); m["reap_close"] != "failed:502" || m["reap_attempts"] != "1" || rep.Failed != 1 || rep.Closed != 1 {
		t.Fatalf("502: pr 103 = %v, %s", m, rep.Summary(S, false))
	}
	if _, err := reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)}); err != nil {
		t.Fatal(err)
	}
	if m := rec(t, ctx, c, 103); m["reap_close"] != "done" || m["state"] != "closed" || f.closed()[103] != 2 {
		t.Fatalf("retry: pr 103 = %v closes %v", m, f.closed())
	}
	// Ten failed closes: STUCK, no call.
	card(t, ctx, c, 120, "card-s", 60, map[string]string{"branch_gone": "1", "reap": reap.RuleBranchGone,
		"reap_close": "failed:502", "reap_attempts": "10"})
	rep, err = reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stuck != 1 || f.closed()[120] != 0 || !strings.Contains(strings.Join(rep.Lines, "\n"), "STUCK "+repo+"#120") {
		t.Fatalf("stuck: %v %s", rep.Lines, rep.Summary(S, false))
	}
}

// TestReapGateNextToEachClose: A and B are both decided; while A's close is
// in flight a live fix for B is pushed. B's gate runs after A's close
// returns, revokes B (reap_close=revoked:...) and B gets no call.
func TestReapGateNextToEachClose(t *testing.T) {
	t.Parallel()

	ctx, c := store(t)
	card(t, ctx, c, 131, "card-b1", 70, map[string]string{"branch_gone": "1"})
	card(t, ctx, c, 130, "card-a1", 71, map[string]string{"branch_gone": "1"})
	f := newForge(t)
	f.during = func(n int) {
		if n == 130 {
			task(t, ctx, c, "fix-131-b", "ready", "fix", 131, "131")
		}
	}
	rep, err := reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.closed(); len(got) != 1 || got[130] != 1 {
		t.Fatalf("closes %v, want 130 only\n%s", got, strings.Join(rep.Lines, "\n"))
	}
	m := rec(t, ctx, c, 131)
	if !strings.HasPrefix(m["reap_close"], "revoked:live fix-131-b") || m["state"] != "open" || rep.Revoked != 1 {
		t.Fatalf("pr 131 = %v, %s", m, rep.Summary(S, false))
	}
	// The next run keeps it (the fix is live) and makes no call.
	rep, err = reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Calls != 0 || rep.Keep != 1 {
		t.Fatalf("after revoke: %s", rep.Summary(S, false))
	}
}

// TestReapFenceRefusesDrift: a record whose head or reads moved after the
// read is not decided (STALE), and a pending decision the policy no longer
// holds is revoked without a call.
func TestReapFenceRefusesDrift(t *testing.T) {
	t.Parallel()

	ctx, c := store(t)
	card(t, ctx, c, 140, "card-x", 80, map[string]string{"branch_gone": "1"})
	key := "pr:nova-tools:140"
	if r, _ := c.FCall(ctx, "ns_pr_reap", nil, S, key, head(140), "3", reap.RuleBranchGone, "branch", "x").Text(); r != "STALE|reads" {
		t.Errorf("reads drift = %q, want STALE|reads", r)
	}
	if r, _ := c.FCall(ctx, "ns_pr_reap", nil, S, key, head(1), "0", reap.RuleBranchGone, "branch", "x").Text(); r != "STALE|head" {
		t.Errorf("head drift = %q, want STALE|head", r)
	}
	if r, _ := c.FCall(ctx, "ns_pr_reap", nil, S, "pr:nova-tools:1", head(1), "0", reap.RuleBranchGone, "branch", "x").Text(); r != "MISSING" {
		t.Errorf("no record = %q, want MISSING", r)
	}
	if r, _ := c.FCall(ctx, "ns_pr_reap", nil, S, key, head(140), "0", reap.RuleBranchGone, "branch", "x").Text(); r != "OK" {
		t.Fatalf("decide = %q", r)
	}
	// An APPROVE lands at head: the pending decision is revoked, no call.
	must(t, c.HSet(ctx, key, "reads", score(140, 9, "stella")).Err())
	f := newForge(t)
	rep, err := reap.Run(ctx, c, reap.Options{Sprint: S, Closer: f.gh(10)})
	if err != nil {
		t.Fatal(err)
	}
	if m := rec(t, ctx, c, 140); !strings.HasPrefix(m["reap_close"], "revoked:approve") || rep.Revoked != 1 || len(f.closed()) != 0 {
		t.Fatalf("approve after decision: %v %s", m, rep.Summary(S, false))
	}
}

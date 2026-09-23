package consume

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// The helpers in this file carry a cls prefix so the package's other control
// files (okfriend_test.go, #2933) can declare their own without a clash.

const (
	clsBase = "09fbedc9"
	clsTip  = "7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a"
)

// clsRedis starts a throwaway redis-server for a control sprint (#2756
// section 8) with the nova_sprint library loaded.
func clsRedis(t *testing.T) *redis.Client {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logf, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logf.Close() })
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", strings.TrimPrefix(addr, "127.0.0.1:"),
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 200})
	t.Cleanup(func() { _ = client.Close() })
	// go-redis retries the dial with backoff until the server listens; the
	// retry count, not a wall-clock bound, decides the give-up.
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("throwaway redis did not start: %v", err)
	}
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return client
}

func clsMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// clsSprint opens a control sprint with the given policy fields and two
// fixture benches.
func clsSprint(t *testing.T, client *redis.Client, sprint string, policy ...string) {
	t.Helper()
	ctx := context.Background()
	clsMust(t, client.HSet(ctx, "s:"+sprint, "status", "open").Err())
	fields := append([]string{"readers", "1"}, policy...)
	args := make([]any, len(fields))
	for i, f := range fields {
		args[i] = f
	}
	clsMust(t, client.HSet(ctx, "s:"+sprint+":policy", args...).Err())
	clsMust(t, client.SAdd(ctx, "benches", "ctl-b1", "ctl-b2", "ctl-b3", "ctl-b4").Err())
}

// clsEnd stands in for ns_card_end (#3011): the card at `attempt` on `bench`
// ends with outcome and reason; the hash, the index move and one `ended`
// receipt in one MULTI. A card that does not exist yet is created with the
// fixture shape; an existing card (a requeue, a fix or recut card) keeps its
// other fields.
func clsEnd(t *testing.T, client *redis.Client, sprint, label string, attempt int, bench, outcome, reason string, extra ...string) {
	t.Helper()
	ctx := context.Background()
	key := "s:" + sprint + ":card:" + label
	card, err := client.HGetAll(ctx, key).Result()
	clsMust(t, err)
	base := card["base_sha"]
	if len(card) == 0 {
		base = clsBase
	}
	if base == "" {
		base = clsTip
	}
	a := strconv.Itoa(attempt)
	identity := fmt.Sprintf("%s/%s/%s/%s/%s", sprint, label, base[:min(8, len(base))], bench, a)
	fields := []any{"state", "ended", "attempt", a, "identity", identity, "bench", bench,
		"outcome", outcome, "reason", reason, "ended_at", "1", "base_sha", base}
	if len(card) == 0 {
		fields = append(fields, "kind", "model", "leg", "go", "tier", "1", "repo", "nova-tools", "base", "dev",
			"paths", "internal/x/", "depends_on", "", "priority", "5", "author", "ctl-a", "retries", "0")
	}
	for _, e := range extra {
		fields = append(fields, e)
	}
	_, err = client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key, fields...)
		for _, from := range []string{"queued", "dealt", "running"} {
			p.SRem(ctx, "s:"+sprint+":idx:card:"+from, label)
		}
		p.ZRem(ctx, "s:"+sprint+":pool", label)
		p.SAdd(ctx, "s:"+sprint+":idx:card:ended", label)
		p.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: []any{
			"kind", "card", "id", label, "from", "running", "to", "ended", "attempt", a,
			"token_sha", "abcdefabcdef", "actor", "card-end", "reason", reason,
			"evidence", identity, "idem", "end:" + identity, "at", "1"}})
		return nil
	})
	clsMust(t, err)
}

// clsPass runs one non-blocking classification pass.
func clsPass(t *testing.T, client *redis.Client, sprint string) int {
	t.Helper()
	c := &Classifier{Store: store.New(client), Sprint: sprint, Consumer: "ctl-classify", Actor: "ok-to-friend",
		Block: -1, Tip: func(context.Context, string, string) (string, error) { return clsTip, nil }}
	n, err := c.Pass(context.Background())
	clsMust(t, err)
	return n
}

func clsCard(t *testing.T, client *redis.Client, sprint, label string) map[string]string {
	t.Helper()
	h, err := client.HGetAll(context.Background(), "s:"+sprint+":card:"+label).Result()
	clsMust(t, err)
	return h
}

func clsUnresolved(t *testing.T, client *redis.Client, sprint string) map[string]string {
	t.Helper()
	h, err := client.HGetAll(context.Background(), "s:"+sprint+":unresolved").Result()
	clsMust(t, err)
	return h
}

// clsCards lists every card label in the sprint.
func clsCards(t *testing.T, client *redis.Client, sprint string) []string {
	t.Helper()
	keys, err := client.Keys(context.Background(), "s:"+sprint+":card:*").Result()
	clsMust(t, err)
	var out []string
	for _, k := range keys {
		out = append(out, strings.TrimPrefix(k, "s:"+sprint+":card:"))
	}
	return out
}

// clsTransitions counts the classification receipts per `label to`.
func clsTransitions(t *testing.T, client *redis.Client, sprint string) map[string]int {
	t.Helper()
	entries, err := client.XRange(context.Background(), "s:"+sprint+":log", "-", "+").Result()
	clsMust(t, err)
	out := map[string]int{}
	for _, e := range entries {
		if e.Values["kind"] == "card" && e.Values["actor"] == "ok-to-friend" {
			out[fmt.Sprint(e.Values["id"])+" "+fmt.Sprint(e.Values["to"])]++
		}
	}
	return out
}

func clsInPool(t *testing.T, client *redis.Client, sprint, label string) (float64, bool) {
	t.Helper()
	score, err := client.ZScore(context.Background(), "s:"+sprint+":pool", label).Result()
	if err == redis.Nil {
		return 0, false
	}
	clsMust(t, err)
	return score, true
}

// TestClassificationTable33 pins the 3.3 table: every outcome and reason
// maps to exactly one action, budget class and interim budget, the budget is
// read from s:<S>:policy, and one Redis pass per row writes that action.
func TestClassificationTable33(t *testing.T) {
	rows := []struct {
		outcome, reason string
		action          string
		policy          string
		budget          int
	}{
		{"FAILED", "crash", ActionRequeue, "retry_crash", 2},
		{"FAILED", "timeout", ActionRequeue, "retry_crash", 2},
		{"FAILED", "idle-killed", ActionRequeue, "retry_crash", 2},
		{"BLOCKED", "env", ActionRequeueEnv, "retry_env", 1},
		{"BLOCKED", "base-moved", ActionRecut, "retry_base_moved", 1},
		{"BLOCKED", "deps", ActionWaiting, "", 0},
		{"FAILED", "tests-red", ActionFix, "retry_tests_red", 1},
		{"ABSTAIN", "scope", ActionUnresolved, "", 0},
		{"BLOCKED", "spec", ActionUnresolved, "", 0},
		{"BLOCKED", "access", ActionUnresolved, "", 0},
		{"FAILED", "other", ActionUnresolved, "", 0},
		{"ABSTAIN", "other", ActionUnresolved, "", 0},
		{"BLOCKED", "no-such-code", ActionUnresolved, "", 0},
		{"FAILED", "", ActionUnresolved, "", 0},
		{"DONE", "", ActionNone, "", 0},
	}
	for _, r := range rows {
		got := ClassifyOutcome(r.outcome, r.reason)
		if got.Action != r.action || got.Policy != r.policy || got.Budget(nil) != r.budget {
			t.Errorf("%s %s: got action=%q policy=%q budget=%d, want %q %q %d",
				r.outcome, r.reason, got.Action, got.Policy, got.Budget(nil), r.action, r.policy, r.budget)
		}
	}
	// The budget is policy, not code: a policy field overrides the interim
	// default; a missing or unreadable field keeps it.
	crash := ClassifyOutcome("FAILED", "crash")
	if n := crash.Budget(map[string]string{"retry_crash": "5"}); n != 5 {
		t.Errorf("retry_crash=5: budget %d", n)
	}
	if n := crash.Budget(map[string]string{"retry_crash": "0"}); n != 0 {
		t.Errorf("retry_crash=0: budget %d", n)
	}
	if n := crash.Budget(map[string]string{"retry_crash": "x"}); n != 2 {
		t.Errorf("retry_crash=x: budget %d, want the interim 2", n)
	}

	client := clsRedis(t)
	const S = "control-3303a3a3"
	clsSprint(t, client, S)
	ctx := context.Background()
	clsEnd(t, client, S, "c-crash", 1, "ctl-b1", "FAILED", "crash")
	clsEnd(t, client, S, "c-env", 1, "ctl-b1", "BLOCKED", "env")
	clsEnd(t, client, S, "c-base", 1, "ctl-b1", "BLOCKED", "base-moved")
	clsEnd(t, client, S, "c-deps", 1, "ctl-b1", "BLOCKED", "deps", "blocked_on", "c-lib")
	clsEnd(t, client, S, "c-red", 1, "ctl-b1", "FAILED", "tests-red")
	clsEnd(t, client, S, "c-scope", 1, "ctl-b1", "ABSTAIN", "scope")
	clsEnd(t, client, S, "c-access", 1, "ctl-b1", "BLOCKED", "access")
	clsEnd(t, client, S, "c-other", 1, "ctl-b1", "FAILED", "other")
	clsEnd(t, client, S, "c-done", 1, "ctl-b1", "DONE", "")
	// A ci card's FAIL verdict follows 10.5, never tests-red.
	clsEnd(t, client, S, "ci-c-x", 1, "ctl-b1", "FAILED", "tests-red", "kind", "script", "ci_for", "nova-tools#1 "+clsTip)
	if n := clsPass(t, client, S); n != 10 {
		t.Fatalf("pass handled %d events, want 10", n)
	}

	// Requeue rows: queued at the front of the pool, the failing bench avoided.
	for _, label := range []string{"c-crash", "c-env"} {
		c := clsCard(t, client, S, label)
		score, ok := clsInPool(t, client, S, label)
		if c["state"] != "queued" || !ok || score >= 0 || c["bench"] != "" || c["avoid_benches"] != "ctl-b1" {
			t.Errorf("%s: state=%q pool=%v score=%v bench=%q avoid=%q, want queued at the front avoiding ctl-b1",
				label, c["state"], ok, score, c["bench"], c["avoid_benches"])
		}
	}
	why, err := client.HGet(ctx, "bench:ctl-b1:why", "env go").Result()
	clsMust(t, err)
	if !strings.HasPrefix(why, "why: env go") {
		t.Errorf("bench why line %q", why)
	}
	// Recut and fix rows: a new front card at the tip, the old superseded.
	for old, fresh := range map[string]string{"c-base": "recut-c-base", "c-red": "fix-c-red"} {
		o, n := clsCard(t, client, S, old), clsCard(t, client, S, fresh)
		score, ok := clsInPool(t, client, S, fresh)
		if o["state"] != "superseded" || o["superseded_by"] != fresh {
			t.Errorf("%s: state=%q superseded_by=%q", old, o["state"], o["superseded_by"])
		}
		if n["state"] != "queued" || n["base_sha"] != clsTip || n["depends_on"] != "" || !ok || score >= 0 || n["parent"] != old {
			t.Errorf("%s: %v pool=%v score=%v, want queued at the front at the tip with no dependency", fresh, n, ok, score)
		}
	}
	// Deps row: back to waiting with the named dependency added.
	d := clsCard(t, client, S, "c-deps")
	isWaiting, err := client.SIsMember(ctx, "s:"+S+":waiting", "c-deps").Result()
	clsMust(t, err)
	if d["state"] != "queued" || !isWaiting || d["depends_on"] != "c-lib" {
		t.Errorf("c-deps: state=%q waiting=%v depends_on=%q", d["state"], isWaiting, d["depends_on"])
	}
	// No-retry rows: one unresolved item each under <label>:<reason>:<base_sha>.
	items := clsUnresolved(t, client, S)
	for _, key := range []string{"c-scope:scope:" + clsBase, "c-access:access:" + clsBase, "c-other:other:" + clsBase} {
		if items[key] == "" {
			t.Errorf("no unresolved item %s in %v", key, items)
		}
	}
	// DONE and the ci card are not this handler's: no transition, no item, no fix card.
	for _, label := range []string{"c-done", "ci-c-x"} {
		if st := clsCard(t, client, S, label)["state"]; st != "ended" {
			t.Errorf("%s moved to %q", label, st)
		}
	}
	for _, label := range clsCards(t, client, S) {
		if label == "fix-ci-c-x" {
			t.Errorf("a ci card's FAIL cut a tests-red fix card")
		}
	}
	for k := range items {
		if strings.HasPrefix(k, "c-done:") || strings.HasPrefix(k, "ci-c-x:") {
			t.Errorf("unresolved item %s for a card this handler does not classify", k)
		}
	}
}

// TestControl16Classification is control 16 of #2756: repeated BLOCKED spec
// events for one card yield one unresolved item; FAILED tests-red yields one
// fix card, then unresolved after its budget; FAILED crash requeues on a
// different bench until its budget, then unresolved; BLOCKED env writes the
// bench why line; the budgets are s:<S>:policy.
func TestControl16Classification(t *testing.T) {
	client := clsRedis(t)
	ctx := context.Background()

	t.Run("spec three events one item", func(t *testing.T) {
		const S = "control-16a16a16"
		clsSprint(t, client, S)
		// Three end events for one card before the consumer runs (a wrapper
		// retrying its end call), then a coordinator re-push that blocks on
		// spec again at attempt 2: still one item under the dedup key.
		for i := 0; i < 3; i++ {
			clsEnd(t, client, S, "s1", 1, "ctl-b1", "BLOCKED", "spec")
		}
		clsPass(t, client, S)
		clsEnd(t, client, S, "s1", 2, "ctl-b2", "BLOCKED", "spec")
		clsPass(t, client, S)
		items := clsUnresolved(t, client, S)
		if len(items) != 1 || items["s1:spec:"+clsBase] == "" {
			t.Fatalf("unresolved items %v, want exactly s1:spec:%s", items, clsBase)
		}
		if n := clsTransitions(t, client, S)["s1 unresolved"]; n != 2 {
			t.Errorf("s1 unresolved receipts %d, want one per ended attempt (2)", n)
		}
	})

	t.Run("tests-red one fix card then unresolved", func(t *testing.T) {
		const S = "control-16b16b16"
		clsSprint(t, client, S)
		clsEnd(t, client, S, "r1", 1, "ctl-b1", "FAILED", "tests-red")
		clsEnd(t, client, S, "r1", 1, "ctl-b1", "FAILED", "tests-red") // redelivered end
		clsPass(t, client, S)
		fix := clsCard(t, client, S, "fix-r1")
		if fix["state"] != "queued" || fix["front"] != "1" || fix["base_sha"] != clsTip || fix["depends_on"] != "" {
			t.Fatalf("fix-r1 = %v, want one front fix card at the tip with no dependency", fix)
		}
		// The fix card runs and its tests are red again: budget 1 is spent.
		clsEnd(t, client, S, "fix-r1", 1, "ctl-b2", "FAILED", "tests-red")
		clsPass(t, client, S)
		for _, label := range clsCards(t, client, S) {
			if label != "r1" && label != "fix-r1" {
				t.Errorf("a second fix card %s was cut", label)
			}
		}
		items := clsUnresolved(t, client, S)
		if len(items) != 1 || items["fix-r1:tests-red:"+clsTip] == "" {
			t.Errorf("unresolved items %v, want exactly fix-r1:tests-red:%s", items, clsTip)
		}
		if st := clsCard(t, client, S, "fix-r1")["state"]; st != "unresolved" {
			t.Errorf("fix-r1 state %q, want unresolved", st)
		}
		if n := clsTransitions(t, client, S)["fix-r1 queued"]; n != 1 {
			t.Errorf("fix-r1 created %d times", n)
		}
	})

	t.Run("crash requeues twice on another bench then unresolved", func(t *testing.T) {
		const S = "control-16c16c16"
		clsSprint(t, client, S)
		benches := []string{"ctl-b1", "ctl-b2", "ctl-b3"}
		for i, bench := range benches {
			// The deal pass honours avoid_benches (#2756 5.3); this control
			// deals each attempt on a bench the card has not failed on.
			if avoid := clsCard(t, client, S, "k1")["avoid_benches"]; strings.Contains(avoid, bench) {
				t.Fatalf("attempt %d dealt on avoided bench %s (%q)", i+1, bench, avoid)
			}
			clsEnd(t, client, S, "k1", i+1, bench, "FAILED", "crash")
			clsPass(t, client, S)
			c := clsCard(t, client, S, "k1")
			if i < 2 {
				want := strings.Join(benches[:i+1], " ")
				if c["state"] != "queued" || c["avoid_benches"] != want || c["retry_crash"] != strconv.Itoa(i+1) {
					t.Fatalf("after crash %d: state=%q avoid=%q retry_crash=%q, want queued avoiding %q",
						i+1, c["state"], c["avoid_benches"], c["retry_crash"], want)
				}
				continue
			}
			if c["state"] != "unresolved" {
				t.Fatalf("after crash 3: state %q, want unresolved", c["state"])
			}
		}
		if items := clsUnresolved(t, client, S); len(items) != 1 || items["k1:crash:"+clsBase] == "" {
			t.Errorf("unresolved items %v", items)
		}
	})

	t.Run("env writes the bench why line", func(t *testing.T) {
		const S = "control-16d16d16"
		clsSprint(t, client, S)
		clsEnd(t, client, S, "e1", 1, "ctl-b4", "BLOCKED", "env", "leg", "zig")
		clsPass(t, client, S)
		why, err := client.HGet(ctx, "bench:ctl-b4:why", "env zig").Result()
		clsMust(t, err)
		if !strings.HasPrefix(why, "why: env zig") || !strings.Contains(why, S+"/e1/1") {
			t.Errorf("bench why line %q", why)
		}
		if item := clsUnresolved(t, client, S)["bench:ctl-b4:env:zig"]; item == "" {
			t.Errorf("no unresolved bench item for ctl-b4 env zig")
		}
		c := clsCard(t, client, S, "e1")
		if c["state"] != "queued" || c["avoid_benches"] != "ctl-b4" {
			t.Errorf("e1 state=%q avoid=%q, want requeued off ctl-b4", c["state"], c["avoid_benches"])
		}
		// Budget 1: a second env block goes unresolved, not a third bench.
		clsEnd(t, client, S, "e1", 2, "ctl-b1", "BLOCKED", "env", "leg", "zig")
		clsPass(t, client, S)
		if st := clsCard(t, client, S, "e1")["state"]; st != "unresolved" {
			t.Errorf("e1 after second env: state %q, want unresolved", st)
		}
	})

	t.Run("budgets come from the policy", func(t *testing.T) {
		const S = "control-16e16e16"
		// retry_crash 3 instead of 2, retry_tests_red 0 instead of 1.
		clsSprint(t, client, S, "retry_crash", "3", "retry_tests_red", "0")
		requeues := 0
		for attempt := 1; attempt <= 5; attempt++ {
			clsEnd(t, client, S, "k2", attempt, fmt.Sprintf("ctl-b%d", (attempt-1)%4+1), "FAILED", "timeout")
			clsPass(t, client, S)
			if clsCard(t, client, S, "k2")["state"] != "queued" {
				break
			}
			requeues++
		}
		if requeues != 3 {
			t.Errorf("retry_crash=3 gave %d requeues", requeues)
		}
		clsEnd(t, client, S, "r2", 1, "ctl-b1", "FAILED", "tests-red")
		clsPass(t, client, S)
		if c := clsCard(t, client, S, "fix-r2"); len(c) != 0 {
			t.Errorf("retry_tests_red=0 still cut fix-r2: %v", c)
		}
		if st := clsCard(t, client, S, "r2")["state"]; st != "unresolved" {
			t.Errorf("r2 state %q, want unresolved at budget 0", st)
		}
	})
}

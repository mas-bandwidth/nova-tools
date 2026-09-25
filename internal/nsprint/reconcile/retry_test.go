package reconcile_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// seedEnded writes one ended card the way the card wrapper's end leaves it
// (card_run.lua: state ended, outcome, reason, exit, pushed_sha), in the ended
// index and the bench's ended set.
func seedEnded(t *testing.T, ctx context.Context, client *redis.Client, sprint, label string, attempt int, fields ...string) {
	t.Helper()
	h := map[string]string{"state": "ended", "attempt": fmt.Sprint(attempt), "retries": "0", "priority": "3",
		"bench": "retry-bench", "token_sha": "sha", "identity": fmt.Sprintf("%s/%s/0123abcd/retry-bench/%d", sprint, label, attempt),
		"outcome": "FAILED", "reason": "idle-killed", "exit": "137", "pushed_sha": ""}
	for i := 0; i+1 < len(fields); i += 2 {
		h[fields[i]] = fields[i+1]
	}
	must(t, client.HSet(ctx, card.CardKey(sprint, label), h).Err())
	must(t, client.SAdd(ctx, card.IdxKey(sprint, h["state"]), label).Err())
	if h["state"] == "ended" {
		must(t, client.SAdd(ctx, card.BenchEndedKey(sprint, "retry-bench"), label).Err())
	}
}

// TestRetryOnceNeverTwice: a card that crashed before leaving any effect
// (idle-killed, or crash with exit -1, no pushed sha) goes back to the pool
// once, with one receipt under retry:<S>/<label>/<attempt>. A replay returns
// that receipt and writes nothing; the same card ending the same way on its
// next attempt stays ended (retry_max 1); a stale fence writes nothing.
func TestRetryOnceNeverTwice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, client := newSprint(t)
	const sprint = "retry-2930a0b1"
	seedEnded(t, ctx, client, sprint, "idle", 1)
	seedEnded(t, ctx, client, sprint, "crash", 1, "reason", "crash", "exit", "-1")

	stale, err := reconcile.Retry(ctx, st, reconcile.RetryRequest{Sprint: sprint, Label: "idle", Fence: "rc-0.old", RetryMax: 1})
	if err != nil || stale.Code != 3 || stateOf(t, ctx, client, sprint, "idle") != "ended" || xlen(t, ctx, client, sprint) != 0 {
		t.Fatalf("stale fence retry: %+v %v", stale, err)
	}

	for _, c := range []struct{ label, reason string }{{"idle", "retry:idle-killed"}, {"crash", "retry:crash"}} {
		res, err := reconcile.Retry(ctx, st, reconcile.RetryRequest{Sprint: sprint, Label: c.label, Fence: fence, RetryMax: 1})
		if err != nil || res.Code != 0 || res.Status != "QUEUED" || res.Receipt == "" || res.Attempt != 1 {
			t.Fatalf("retry %s: %+v %v", c.label, res, err)
		}
		h := hashOf(t, ctx, client, sprint, c.label)
		if h["state"] != "queued" || h["reason"] != c.reason || h["retry_of"] != "1" || h["retries"] != "1" ||
			h["retried_at"] == "" || h["token"] != "" || h["attempt"] != "1" || h["outcome"] != "FAILED" {
			t.Fatalf("%s after retry: %v", c.label, h)
		}
		if !inPool(t, ctx, client, sprint, c.label) || !setHas(t, ctx, client, card.IdxKey(sprint, "queued"), c.label) ||
			setHas(t, ctx, client, card.IdxKey(sprint, "ended"), c.label) ||
			setHas(t, ctx, client, card.BenchEndedKey(sprint, "retry-bench"), c.label) {
			t.Fatalf("%s: indexes not moved to queued", c.label)
		}
		idem, _ := client.HGet(ctx, card.IdemKey(sprint), "retry:"+sprint+"/"+c.label+"/1").Result()
		if idem != res.Receipt {
			t.Fatalf("%s: idem key = %q, want the receipt %q", c.label, idem, res.Receipt)
		}
		n := xlen(t, ctx, client, sprint)
		again, err := reconcile.Retry(ctx, st, reconcile.RetryRequest{Sprint: sprint, Label: c.label, Fence: fence, RetryMax: 1})
		if err != nil || again.Code != 0 || again.Status != "OK" || again.Receipt != res.Receipt || xlen(t, ctx, client, sprint) != n {
			t.Fatalf("%s replay: %+v %v (log %d -> %d)", c.label, again, err, n, xlen(t, ctx, client, sprint))
		}
		if got := receiptsTo(t, ctx, client, sprint, c.label, "queued"); got != 1 {
			t.Fatalf("%s: queued receipts = %d, want 1", c.label, got)
		}
	}

	// The deal took attempt 2, and it crashed the same way: never a third attempt.
	must(t, client.SRem(ctx, card.IdxKey(sprint, "queued"), "idle").Err())
	must(t, client.ZRem(ctx, "s:"+sprint+":pool", "idle").Err())
	seedEnded(t, ctx, client, sprint, "idle", 2, "retries", "1", "retry_of", "1")
	n := xlen(t, ctx, client, sprint)
	second, err := reconcile.Retry(ctx, st, reconcile.RetryRequest{Sprint: sprint, Label: "idle", Fence: fence, RetryMax: 1})
	if err != nil || second.Status != "NOTHING" || stateOf(t, ctx, client, sprint, "idle") != "ended" ||
		inPool(t, ctx, client, sprint, "idle") || xlen(t, ctx, client, sprint) != n {
		t.Fatalf("second crash at retry_max: %+v %v", second, err)
	}
	if reconcile.Retryable(hashOf(t, ctx, client, sprint, "idle"), 1) {
		t.Fatal("Retryable: true for a card whose retries reached retry_max")
	}
}

// TestRetryNeverOnEffectOrFinding: an end that may have left an effect (a
// pushed sha, a timeout, a crash with a real exit code) or that is a finding
// (tests red, BLOCKED, ABSTAIN, DONE), and every card that is not ended, is
// never fed back: NOTHING, no hash change, no receipt.
func TestRetryNeverOnEffectOrFinding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, client := newSprint(t)
	const sprint = "retry-2930c0d1"
	cases := []struct {
		label  string
		fields []string
	}{
		{"tests-red", []string{"reason", "tests-red", "exit", "1"}},
		{"timeout", []string{"reason", "timeout", "exit", "-1"}},
		{"crash-exit-1", []string{"reason", "crash", "exit", "1"}},
		{"pushed", []string{"pushed_sha", "89abcdef0123456789abcdef0123456789abcdef"}},
		{"blocked", []string{"outcome", "BLOCKED", "reason", "blocked", "exit", "0"}},
		{"abstain", []string{"outcome", "ABSTAIN", "reason", "abstain", "exit", "0"}},
		{"done", []string{"outcome", "DONE", "reason", "done", "exit", "0"}},
		{"running", []string{"state", "running"}},
		{"required", []string{"state", "reconcile-required"}},
		{"orphan", []string{"state", "orphan-effect"}},
		{"zero-max", nil},
	}
	for _, c := range cases {
		seedEnded(t, ctx, client, sprint, c.label, 1, c.fields...)
	}
	for _, c := range cases {
		retryMax := 1
		if c.label == "zero-max" {
			retryMax = 0
		}
		before := hashOf(t, ctx, client, sprint, c.label)
		n := xlen(t, ctx, client, sprint)
		res, err := reconcile.Retry(ctx, st, reconcile.RetryRequest{Sprint: sprint, Label: c.label, Fence: fence, RetryMax: retryMax})
		if err != nil || res.Code != 0 || res.Status != "NOTHING" {
			t.Errorf("%s: %+v %v, want NOTHING", c.label, res, err)
			continue
		}
		sameHash(t, c.label, before, hashOf(t, ctx, client, sprint, c.label))
		if xlen(t, ctx, client, sprint) != n || inPool(t, ctx, client, sprint, c.label) {
			t.Errorf("%s: a receipt or a pool entry was written", c.label)
		}
		if before["state"] == "ended" && reconcile.Retryable(before, retryMax) {
			t.Errorf("%s: Retryable true, want false", c.label)
		}
	}
}

// TestParsePolicyDefaults: an absent or unreadable s:<S>:policy field reads
// as its default (60000/180000/1/60000/10000); retry_max 0 is a policy.
func TestParsePolicyDefaults(t *testing.T) {
	t.Parallel()

	if p := reconcile.ParsePolicy(nil); p != reconcile.DefaultPolicy ||
		p.Start.Milliseconds() != 60000 || p.Beat.Milliseconds() != 180000 || p.RetryMax != 1 ||
		p.Open.Milliseconds() != 60000 || p.ExpireEvery.Milliseconds() != 10000 {
		t.Fatalf("defaults: %+v", p)
	}
	p := reconcile.ParsePolicy(map[string]string{"start_ms": "5", "beat_ms": "x", "retry_max": "0", "open_ms": "-3", "expire_every_ms": "20000"})
	if p.Start.Milliseconds() != 5 || p.Beat != reconcile.DefaultPolicy.Beat || p.RetryMax != 0 ||
		p.Open != reconcile.DefaultPolicy.Open || p.ExpireEvery.Milliseconds() != 20000 {
		t.Fatalf("parsed: %+v", p)
	}
}

// TestRetryAfterWrapperNoCommitEnd: the card wrapper ends every non-DONE
// attempt with pushed_sha "-" (card.NoCommit, finish in card/wrapper.go), so
// a harness SIGKILLed before any effect reaches the ended index as FAILED
// crash exit -1 pushed_sha "-". That card is fed back once, exactly as one
// with no pushed_sha field: "-" is no commit, not an effect. A real sha stays
// NOTHING (TestRetryNeverOnEffectOrFinding). #2930 probe step 2 (queued
// retry:crash a1) needs this.
func TestRetryAfterWrapperNoCommitEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, client := newSprint(t)
	const sprint = "retry-2930e0f1"
	seedEnded(t, ctx, client, sprint, "killed", 1, "reason", "crash", "exit", "-1", "pushed_sha", card.NoCommit)
	seedEnded(t, ctx, client, sprint, "idle", 1, "pushed_sha", card.NoCommit)
	for _, c := range []struct{ label, reason string }{{"killed", "retry:crash"}, {"idle", "retry:idle-killed"}} {
		if !reconcile.Retryable(hashOf(t, ctx, client, sprint, c.label), 1) {
			t.Errorf("%s: Retryable false for pushed_sha %q, want true", c.label, card.NoCommit)
		}
		res, err := reconcile.Retry(ctx, st, reconcile.RetryRequest{Sprint: sprint, Label: c.label, Fence: fence, RetryMax: 1})
		if err != nil || res.Code != 0 || res.Status != "QUEUED" || res.Receipt == "" {
			t.Errorf("%s: %+v %v, want QUEUED", c.label, res, err)
			continue
		}
		h := hashOf(t, ctx, client, sprint, c.label)
		if h["state"] != "queued" || h["reason"] != c.reason || h["retry_of"] != "1" || !inPool(t, ctx, client, sprint, c.label) {
			t.Errorf("%s after retry: %v", c.label, h)
		}
		if got := receiptsTo(t, ctx, client, sprint, c.label, "queued"); got != 1 {
			t.Errorf("%s: queued receipts = %d, want 1", c.label, got)
		}
	}
}

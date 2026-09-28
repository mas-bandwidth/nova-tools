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

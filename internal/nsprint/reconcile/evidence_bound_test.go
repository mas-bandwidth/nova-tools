package reconcile_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// slowProber is a Prober whose Probe sleeps for Delay before answering.
type slowProber struct {
	Delay time.Duration
}

func (p *slowProber) Probe(ctx context.Context, b deal.Bench, cards []reconcile.Suspect) (map[string]reconcile.Evidence, error) {
	select {
	case <-time.After(p.Delay):
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestEvidenceBoundedByLease is nova-tools #3802: the reconciler pass must
// never block on the evidence ssh. The evidence session is bounded by the
// lease's remaining time less the write margin, so a wedged or slow sshd
// is cut before the pass can fence. A 5 s fake ssh shows the pass returning
// well under 5 s (bounded by the lease budget).
func TestEvidenceBoundedByLease(t *testing.T) {
	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil && err != redis.Nil {
			t.Fatal(err)
		}
	}
	const S, bench = "evidence-bounded-3802", "slow-bench"
	must(c.SAdd(ctx, "sprints", S).Err())
	must(c.HSet(ctx, "s:"+S, "status", "open").Err())
	must(c.HSet(ctx, reconcile.PolicyKey(S), "share", "1").Err())
	must(c.SAdd(ctx, "benches", bench).Err())
	must(c.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1").Err())

	tm, err := c.Time(ctx).Result()
	must(err)
	now := tm.UnixMilli()
	must(c.HSet(ctx, card.CardKey(S, "stuck"), map[string]any{
		"state": "reconcile-required", "attempt": "1", "bench": bench,
		"token_sha": "sha-stuck", "identity": S + "/stuck/0123abcd/" + bench + "/1",
		"reason": "beat-lost", "required_at": strconv.FormatInt(now-2*3600*1000, 10),
		"branch": "nova/" + S + "/stuck", "repo": "ctl-org/ctl-repo", "jobdir": "/j/stuck",
	}).Err())
	must(c.SAdd(ctx, card.IdxKey(S, "reconcile-required"), "stuck").Err())

	// A short TTL so the evidence budget is small; the 5 s fake ssh will
	// be cut well before the pass can fence.
	ttl := 2 * time.Second
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{
		Host: "ctl-3802", TTL: ttl, Heartbeat: false,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// The fake prober takes 5 s, but the evidence budget is the lease
	// remaining (close to 2 s) less the 1 s write margin = about 1 s.
	prober := &slowProber{Delay: 5 * time.Second}
	duty := &reconcile.Expire{Client: c, Prober: prober}

	start := time.Now()
	_, err = duty.Run(ctx, l)
	took := time.Since(start)

	if err != nil {
		t.Fatalf("sweep with slow evidence: %v", err)
	}
	// The pass must return well under the 5 s the prober would have taken;
	// with a 2 s TTL and 1 s margin the budget is ~1 s.
	if took >= 5*time.Second {
		t.Fatalf("pass took %s with a 5 s fake ssh; the evidence session must be bounded by the lease budget", took)
	}
}

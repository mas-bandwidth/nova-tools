package deal_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

func miniStore(t *testing.T) (*miniredis.Miniredis, *store.Store, *redis.Client) {
	t.Helper()
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return m, store.New(c), c
}

// TestControl11BackpressureMissingAppliesPolicy is control 11 of nova-sprint
// (#2756 section 8, issue #2725): with s:<S>:backpressure missing the sprint's
// declared backpressure_missing policy applies and the verdict prints it; a
// stale hash (older than 20 s) is treated as missing; an undeclared policy is
// refused; and exactly one backpressure key exists for the sprint.
// Antecedent: the dealer read one BACKPRESSURE file while the loop wrote
// another, and a stopped loop left ON on disk, failing closed with no signal.
func TestControl11BackpressureMissingAppliesPolicy(t *testing.T) {
	ctx := context.Background()
	const sprint = "control-0000c011"
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	if got, want := deal.BackpressureKey(sprint), "s:"+sprint+":backpressure"; got != want {
		t.Fatalf("BackpressureKey = %q, want %q", got, want)
	}

	t.Run("missing, policy open: bulk flows and the line says so", func(t *testing.T) {
		m, st, c := miniStore(t)
		m.SetTime(now)
		c.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "open")
		v, err := deal.ReadBackpressure(ctx, st, sprint)
		if err != nil {
			t.Fatal(err)
		}
		if v.Source != deal.SourceMissing || v.Blocked {
			t.Fatalf("verdict = %+v, want missing and not blocked", v)
		}
		if got, want := v.Line(), "backpressure: missing, policy open"; got != want {
			t.Fatalf("Line = %q, want %q", got, want)
		}
		if v.DealerLine() != "" {
			t.Fatalf("DealerLine = %q, want empty", v.DealerLine())
		}
		if !v.Allows(deal.TierBulk) || !v.Allows(deal.TierPriority) {
			t.Fatal("policy open must let both tiers flow")
		}
	})

	t.Run("missing, policy closed: only the priority tier flows", func(t *testing.T) {
		m, st, c := miniStore(t)
		m.SetTime(now)
		c.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "closed")
		v, err := deal.ReadBackpressure(ctx, st, sprint)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := v.Line(), "backpressure: missing, policy closed"; got != want {
			t.Fatalf("Line = %q, want %q", got, want)
		}
		if !v.Blocked || v.Allows(deal.TierBulk) || !v.Allows(deal.TierPriority) {
			t.Fatalf("verdict = %+v, want bulk held and priority flowing", v)
		}
		if got, want := v.DealerLine(), "dealer: blocked backpressure missing, policy closed"; got != want {
			t.Fatalf("DealerLine = %q, want %q", got, want)
		}
	})

	t.Run("undeclared policy is refused, never guessed", func(t *testing.T) {
		m, st, _ := miniStore(t)
		m.SetTime(now)
		_, err := deal.ReadBackpressure(ctx, st, sprint)
		if !errors.Is(err, deal.ErrPolicyUndeclared) {
			t.Fatalf("err = %v, want ErrPolicyUndeclared", err)
		}
		m2, st2, c2 := miniStore(t)
		m2.SetTime(now)
		c2.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "maybe")
		if _, err := deal.ReadBackpressure(ctx, st2, sprint); !errors.Is(err, deal.ErrPolicyUndeclared) {
			t.Fatalf("err = %v, want ErrPolicyUndeclared for an unknown value", err)
		}
	})

	t.Run("fresh ON holds bulk and names debt and cap", func(t *testing.T) {
		m, st, c := miniStore(t)
		m.SetTime(now)
		c.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "open")
		c.HSet(ctx, deal.BackpressureKey(sprint), "state", "ON", "debt", "185", "cap", "100", "at", now.Add(-3*time.Second).UnixMilli())
		v, err := deal.ReadBackpressure(ctx, st, sprint)
		if err != nil {
			t.Fatal(err)
		}
		if v.Source != deal.SourceHash || !v.Blocked || v.Allows(deal.TierBulk) || !v.Allows(deal.TierPriority) {
			t.Fatalf("verdict = %+v, want hash ON holding bulk", v)
		}
		if got, want := v.Line(), "backpressure: ON debt=185 cap=100 age=3s"; got != want {
			t.Fatalf("Line = %q, want %q", got, want)
		}
		if got, want := v.DealerLine(), "dealer: blocked debt=185 cap=100"; got != want {
			t.Fatalf("DealerLine = %q, want %q", got, want)
		}
	})

	t.Run("fresh OFF lets bulk flow", func(t *testing.T) {
		m, st, c := miniStore(t)
		m.SetTime(now)
		c.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "closed")
		c.HSet(ctx, deal.BackpressureKey(sprint), "state", "OFF", "debt", "4", "cap", "100", "at", now.Add(-1*time.Second).UnixMilli())
		v, err := deal.ReadBackpressure(ctx, st, sprint)
		if err != nil {
			t.Fatal(err)
		}
		if v.Blocked || !v.Allows(deal.TierBulk) || v.Line() != "backpressure: OFF debt=4 cap=100 age=1s" {
			t.Fatalf("verdict = %+v line %q, want OFF flowing", v, v.Line())
		}
	})

	t.Run("a stopped loop's stale ON does not fail closed silently", func(t *testing.T) {
		m, st, c := miniStore(t)
		m.SetTime(now)
		c.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "open")
		c.HSet(ctx, deal.BackpressureKey(sprint), "state", "ON", "debt", "185", "cap", "100", "at", now.Add(-25*time.Second).UnixMilli())
		v, err := deal.ReadBackpressure(ctx, st, sprint)
		if err != nil {
			t.Fatal(err)
		}
		if v.Source != deal.SourceStale || v.Blocked || !v.Allows(deal.TierBulk) {
			t.Fatalf("verdict = %+v, want stale under policy open, not blocked", v)
		}
		if got, want := v.Line(), "backpressure: stale 25s, policy open"; got != want {
			t.Fatalf("Line = %q, want %q", got, want)
		}
	})

	t.Run("a hash without a readable state or at applies the policy", func(t *testing.T) {
		v, err := deal.EvaluateBackpressure(map[string]string{"state": "SOMETIMES", "at": "x"}, "open", now.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if v.Source != deal.SourceInvalid || v.Blocked || !strings.HasPrefix(v.Line(), "backpressure: invalid") || !strings.HasSuffix(v.Line(), ", policy open") {
			t.Fatalf("verdict = %+v line %q, want invalid under policy open", v, v.Line())
		}
	})

	t.Run("exactly one backpressure key exists", func(t *testing.T) {
		m, st, c := miniStore(t)
		m.SetTime(now)
		c.HSet(ctx, deal.PolicyKey(sprint), "backpressure_missing", "open")
		// Missing is allowed: zero keys is the declared-policy case above.
		if keys, err := deal.CheckOneBackpressureKey(ctx, st, sprint); err != nil || len(keys) != 0 {
			t.Fatalf("missing: keys %v err %v, want none and no error", keys, err)
		}
		c.HSet(ctx, deal.BackpressureKey(sprint), "state", "OFF", "debt", "0", "cap", "100", "at", now.UnixMilli())
		// Another sprint's own hash and the backpressure process beat are not
		// second keys for this sprint.
		c.HSet(ctx, "s:other-sprint:backpressure", "state", "ON")
		c.HSet(ctx, "proc:backpressure", "pass_at", now.UnixMilli())
		keys, err := deal.CheckOneBackpressureKey(ctx, st, sprint)
		if err != nil {
			t.Fatalf("one key: %v", err)
		}
		if len(keys) != 1 || keys[0] != deal.BackpressureKey(sprint) {
			t.Fatalf("keys = %v, want exactly [%s]", keys, deal.BackpressureKey(sprint))
		}
		// The v1 global key is a second source of truth: refused, named.
		c.HSet(ctx, "backpressure", "state", "ON")
		_, err = deal.CheckOneBackpressureKey(ctx, st, sprint)
		if !errors.Is(err, deal.ErrTwoBackpressureKeys) || !strings.Contains(err.Error(), `"backpressure" beside`) {
			t.Fatalf("err = %v, want ErrTwoBackpressureKeys naming the global key", err)
		}
	})
}

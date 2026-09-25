package deal_test

import (
	"context"
	"errors"
	"fmt"
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
		if kc, err := deal.CheckOneBackpressureKey(ctx, st, sprint); err != nil || len(kc.Own) != 0 {
			t.Fatalf("missing: check %+v err %v, want none and no error", kc, err)
		}
		c.HSet(ctx, deal.BackpressureKey(sprint), "state", "OFF", "debt", "0", "cap", "100", "at", now.UnixMilli())
		// Another sprint's own hash and the backpressure process beat are not
		// second keys for this sprint.
		c.HSet(ctx, "s:other-sprint:backpressure", "state", "ON")
		c.HSet(ctx, "proc:backpressure", "pass_at", now.UnixMilli())
		kc, err := deal.CheckOneBackpressureKey(ctx, st, sprint)
		if err != nil {
			t.Fatalf("one key: %v", err)
		}
		if len(kc.Own) != 1 || kc.Own[0] != deal.BackpressureKey(sprint) || !kc.Beat {
			t.Fatalf("check = %+v, want exactly [%s] and the beat", kc, deal.BackpressureKey(sprint))
		}
		if got, want := kc.Line(), "BACKPRESSURE CHECK OK sprint="+sprint+" own=1 beat=1 legacy=0 round_trips=1"; got != want {
			t.Fatalf("Line = %q, want %q", got, want)
		}
		// The v1 global key is a second source of truth: refused, named.
		c.HSet(ctx, "backpressure", "state", "ON")
		kc, err = deal.CheckOneBackpressureKey(ctx, st, sprint)
		if !errors.Is(err, deal.ErrTwoBackpressureKeys) || !strings.Contains(err.Error(), `"backpressure" beside`) {
			t.Fatalf("err = %v, want ErrTwoBackpressureKeys naming the global key", err)
		}
		if got, want := kc.Line(), "BACKPRESSURE CHECK REFUSED sprint="+sprint+" own=1 beat=1 legacy=backpressure round_trips=1"; got != want {
			t.Fatalf("Line = %q, want %q", got, want)
		}
	})
}

// roundTripHook counts Redis round trips (one per single command, one per
// pipeline) and records every command name sent.
type roundTripHook struct {
	roundTrips int
	names      []string
}

func (h *roundTripHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *roundTripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.roundTrips++
		h.names = append(h.names, strings.ToLower(cmd.Name()))
		return next(ctx, cmd)
	}
}

func (h *roundTripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.roundTrips++
		for _, cmd := range cmds {
			h.names = append(h.names, strings.ToLower(cmd.Name()))
		}
		return next(ctx, cmds)
	}
}

// TestCheckOneBackpressureKeyOneRoundTrip is the DONE-WHEN of #3276: with
// 6,000 unrelated keys (and other sprints' backpressure hashes) the check
// issues exactly one pipeline, no SCAN and no KEYS, and the receipt prints
// round_trips=1. The SCAN walk it replaced took 7 round trips at 6,357 keys.
func TestCheckOneBackpressureKeyOneRoundTrip(t *testing.T) {
	ctx := context.Background()
	const sprint = "control-00003276"
	m, st, c := miniStore(t)
	for i := 0; i < 6000; i++ {
		m.Set(fmt.Sprintf("task:unrelated-%05d", i), "x")
	}
	for i := 0; i < 20; i++ {
		m.HSet(fmt.Sprintf("s:sprint-%02d:backpressure", i), "state", "OFF")
	}
	m.HSet(deal.BackpressureKey(sprint), "state", "OFF")
	m.HSet("backpressure", "state", "ON")

	// Dial first so the connection handshake (HELLO, CLIENT SETINFO) is not
	// counted as the check's.
	if err := c.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	hook := &roundTripHook{}
	c.AddHook(hook)
	before := m.CommandCount()
	kc, err := deal.CheckOneBackpressureKey(ctx, st, sprint)
	if !errors.Is(err, deal.ErrTwoBackpressureKeys) {
		t.Fatalf("err = %v, want ErrTwoBackpressureKeys for the legacy key", err)
	}
	if hook.roundTrips != 1 {
		t.Fatalf("round trips = %d (%v), want exactly 1 pipeline", hook.roundTrips, hook.names)
	}
	for _, name := range hook.names {
		if name == "scan" || name == "keys" {
			t.Fatalf("check issued %s (%v); want the named keys only", name, hook.names)
		}
	}
	// miniredis's own count: exactly the named EXISTS, nothing else.
	if got, want := m.CommandCount()-before, 2+len(deal.LegacyBackpressureKeys); got != want {
		t.Fatalf("server saw %d commands (%v), want %d", got, hook.names, want)
	}
	if kc.RoundTrips != 1 || !strings.HasSuffix(kc.Line(), " round_trips=1") {
		t.Fatalf("receipt %q, want round_trips=1", kc.Line())
	}
	// We didn't set proc:backpressure, so beat is false. Let's test with all recognized forms.
	m.HSet("proc:backpressure", "pass_at", "123")
	kcWithBeat, _ := deal.CheckOneBackpressureKey(ctx, st, sprint)

	if len(kcWithBeat.Own) != 1 || !kcWithBeat.Beat || len(kcWithBeat.Extra) != 1 || kcWithBeat.Extra[0] != "backpressure" {
		t.Fatalf("check = %+v, want own=1 beat=true extra=[backpressure]", kcWithBeat)
	}
}

package deal_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

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
	t.Parallel()

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

package harvest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFleetHarvestSkipsNonUp verifies that ns_harvest_due refuses when beat is present
// but state is DOWN, PROBING, or HELD, leaving ended cards untouched, and accepts when state is UP.
func TestFleetHarvestSkipsNonUp(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	const S, bench, label = "sprint-harvest-1", "ctl-h", "card-h1"
	c.SAdd(ctx, "benches", bench)
	c.HSet(ctx, "bench:"+bench+":beat", "host", "localhost", "user", "nova", "at", "1000")

	// Seed an ended card
	identity := S + "/" + label + "/09fbedc9/" + bench + "/1"
	pushedSHA := "1234567890abcdef1234567890abcdef12345678"
	if err := c.HSet(ctx, "s:"+S+":card:"+label,
		"kind", "task", "repo", "nova-tools", "base", "dev", "base_sha", "09fbedc9",
		"state", "ended", "outcome", "DONE", "reason", "done", "bench", bench,
		"attempt", "1", "identity", identity, "token_sha", "abcdef012345",
		"pushed_sha", pushedSHA, "results", identity).Err(); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "s:"+S+":idx:card:ended", label)
	c.SAdd(ctx, "s:"+S+":bench:"+bench+":ended", label)

	for _, nonUp := range []string{"DOWN", "PROBING", "HELD"} {
		c.HSet(ctx, "bench:"+bench+":state", "state", nonUp, "at", "1000")
		reply, err := c.FCallRO(ctx, "ns_harvest_due", nil, S, bench, 256).StringSlice()
		if err != nil {
			t.Fatalf("harvest due on %s: %v", nonUp, err)
		}
		if len(reply) < 2 || reply[0] != "NONE" || reply[1] != strings.ToLower(nonUp) {
			t.Fatalf("harvest due on %s got %v; want [NONE %s]", nonUp, reply, strings.ToLower(nonUp))
		}
		// Ended sets remain untouched
		isEnded := c.SIsMember(ctx, "s:"+S+":bench:"+bench+":ended", label).Val()
		if !isEnded {
			t.Fatalf("card %s unexpectedly removed from bench ended set on %s", label, nonUp)
		}
	}

	// Control: state UP accepts
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1000")
	reply, err := c.FCallRO(ctx, "ns_harvest_due", nil, S, bench, 256).StringSlice()
	if err != nil {
		t.Fatalf("harvest due on UP: %v", err)
	}
	if len(reply) < 3 || reply[0] != "OK" {
		t.Fatalf("harvest due on UP got %v; want OK ...", reply)
	}
}

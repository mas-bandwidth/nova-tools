package deal_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestFleetDealRefusesNonUp verifies that ns_card_deal refuses when beat is present
// but state is DOWN, PROBING, or HELD, and accepts when state is UP.
func TestFleetDealRefusesNonUp(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	const S, bench, label, token = "sprint-1", "ctl-b", "card-1", "deal-token"
	c.HSet(ctx, "lease:reconciler", "token", token, "instance", "inst-1", "host", "host-1")
	c.SAdd(ctx, "sprints", S)
	c.HSet(ctx, "s:"+S, "status", "open")
	c.SAdd(ctx, "benches", bench)
	c.HSet(ctx, "bench:"+bench+":desired", "slots", "5", "paused", "0", "legs", "go")
	c.HSet(ctx, "bench:"+bench+":beat", "host", "localhost", "user", "nova", "at", "1000")

	// Set up a dealable card in queue
	c.HSet(ctx, "s:"+S+":card:"+label, "state", "queued", "attempt", "0", "bench", bench, "leg", "go", "base_sha", "123456789012")
	c.ZAdd(ctx, "s:"+S+":pool", redis.Z{Score: 1, Member: label})
	c.SAdd(ctx, "s:"+S+":idx:card:queued", label)

	ctoken := "1." + strings.Repeat("a", 32)
	csha := "123456789012"

	for _, nonUp := range []string{"DOWN", "PROBING", "HELD"} {
		c.HSet(ctx, "bench:"+bench+":state", "state", nonUp, "at", "1000")
		reply, err := c.FCall(ctx, "ns_card_deal", nil, bench, "deal-token", "tester", "", S, label, 1, ctoken, csha).StringSlice()
		if err != nil {
			t.Fatalf("deal on %s: %v", nonUp, err)
		}
		if len(reply) < 2 || reply[0] != "NONE" || reply[1] != strings.ToLower(nonUp) {
			t.Fatalf("deal on %s got %v; want [NONE %s]", nonUp, reply, strings.ToLower(nonUp))
		}
	}

	// Control: state UP accepts
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1000")
	reply, err := c.FCall(ctx, "ns_card_deal", nil, bench, "deal-token", "tester", "", S, label, 1, ctoken, csha).StringSlice()
	if err != nil {
		t.Fatalf("deal on UP: %v", err)
	}
	if len(reply) == 0 || reply[0] != "DEALT" {
		t.Fatalf("deal on UP got %v; want DEALT", reply)
	}
}

// TestFleetPassUpFromState verifies that deal.Pass sets Up based on bench state == UP.
func TestFleetPassUpFromState(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	const bench = "ctl-p"
	c.SAdd(ctx, "benches", bench)
	c.HSet(ctx, "bench:"+bench+":desired", "slots", "5", "paused", "0")
	c.HSet(ctx, "bench:"+bench+":beat", "host", "localhost", "user", "nova", "at", "1000")

	src := deal.RedisSource{Client: c}

	for _, nonUp := range []string{"DOWN", "PROBING", "HELD"} {
		c.HSet(ctx, "bench:"+bench+":state", "state", nonUp, "at", "1000")
		in, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read input on %s: %v", nonUp, err)
		}
		if len(in.Benches) != 1 {
			t.Fatalf("expected 1 bench, got %d", len(in.Benches))
		}
		if in.Benches[0].Up {
			t.Fatalf("bench Up is true on state %s; want false", nonUp)
		}
	}

	// Control: state UP -> Up is true
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1000")
	in, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read input on UP: %v", err)
	}
	if len(in.Benches) != 1 {
		t.Fatalf("expected 1 bench, got %d", len(in.Benches))
	}
	if !in.Benches[0].Up {
		t.Fatalf("bench Up is false on state UP; want true")
	}
}

package card_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/redis/go-redis/v9"
)

// seedBenches registers UP benches with free slots and opens the test sprint,
// in the shape the deal pass and ns_card_deal read (nova-tools#3650).
func seedBenches(t *testing.T, ctx context.Context, client *redis.Client, names ...string) {
	t.Helper()
	pipe := client.Pipeline()
	for _, b := range names {
		pipe.SAdd(ctx, "benches", b)
		pipe.HSet(ctx, "bench:"+b+":desired", "slots", "4")
		pipe.HSet(ctx, "bench:"+b+":beat", "host", b, "at", "1")
		pipe.HSet(ctx, "bench:"+b+":state", "state", "UP", "at", "1")
	}
	pipe.SAdd(ctx, "sprints", sprint)
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	pipe.HSet(ctx, "s:"+sprint, "status", "open")
	pipe.HSet(ctx, "lease:reconciler", "instance", "ctl", "token", "fence", "at", "1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCardPushBenchStoresThePin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	seedBenches(t, ctx, client, "hulk", "vision")

	f := validCard(srv.URL + "/acme/public.git")
	f.label = "pinned-hulk"
	res := card.Push(ctx, client, sprint, withHeader(f, "BENCH: hulk"))
	if res.Code != 0 || !strings.Contains(res.Stdout, "place=pool") {
		t.Fatalf("BENCH: hulk: exit %d stdout %q stderr %q, want pool", res.Code, res.Stdout, res.Stderr)
	}
	if got, err := client.HGet(ctx, keyCard(f.label), "bench").Result(); err != nil || got != "hulk" {
		t.Fatalf("bench field %q (%v), want hulk", got, err)
	}

	f = validCard(srv.URL + "/acme/public.git")
	f.label = "unpinned"
	if res := card.Push(ctx, client, sprint, withHeader(f)); res.Code != 0 {
		t.Fatalf("no BENCH: exit %d stderr %q", res.Code, res.Stderr)
	}
	if n, _ := client.HExists(ctx, keyCard(f.label), "bench").Result(); n {
		t.Fatalf("a card with no BENCH: line stored a bench pin")
	}
}

func TestCardPushRefusesUnregisteredBench(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	seedBenches(t, ctx, client, "hulk", "vision")
	for i, line := range []string{"BENCH: nosuch", "BENCH:", "BENCH: two words"} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = "bad-bench-" + string(rune('a'+i))
		res := card.Push(ctx, client, sprint, withHeader(f, line))
		if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "BENCH") ||
			!strings.Contains(res.Stderr, "drop the BENCH: line") {
			t.Fatalf("%q: exit %d stdout %q stderr %q, want exit 2 naming BENCH and the remedy", line, res.Code, res.Stdout, res.Stderr)
		}
		if line == "BENCH: nosuch" && !strings.Contains(res.Stderr, "registered: hulk,vision") {
			t.Fatalf("%q: stderr %q, want the registered benches named", line, res.Stderr)
		}
		assertAbsent(t, ctx, client, f.label)
	}
}

// TestDealHonoursPushedBench pushes a card with BENCH: hulk through card push,
// reads the pool the way the deal pass does, and deals: a pass for vision
// skips it (planner and ns_card_deal both), a pass for hulk deals it.
func TestDealHonoursPushedBench(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	seedBenches(t, ctx, client, "hulk", "vision")
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "pinned-hulk"
	if res := card.Push(ctx, client, sprint, withHeader(f, "BENCH: hulk")); res.Code != 0 {
		t.Fatalf("push: exit %d stderr %q", res.Code, res.Stderr)
	}

	in, err := deal.RedisSource{Client: client}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pool []deal.Card
	for _, s := range in.Sprints {
		if s.Name == sprint {
			pool = s.Pool
		}
	}
	if len(pool) != 1 || pool[0].Bench != "hulk" {
		t.Fatalf("pool %+v, want the one card pinned to hulk", pool)
	}
	only := func(name string) deal.Input {
		out := in
		out.Benches = nil
		for _, b := range in.Benches {
			if b.Name == name {
				out.Benches = append(out.Benches, b)
			}
		}
		return out
	}
	if plan := deal.Plan(only("vision"), deal.DefaultRefusedHold); len(plan) != 0 {
		t.Fatalf("vision pass planned %+v, want nothing: the card is pinned to hulk", plan)
	}
	plan := deal.Plan(only("hulk"), deal.DefaultRefusedHold)
	if len(plan) != 1 || plan[0].Bench.Name != "hulk" || len(plan[0].Cards) != 1 || plan[0].Cards[0].Label != f.label {
		t.Fatalf("hulk pass planned %+v, want the pinned card", plan)
	}

	// ns_card_deal holds the pin independently of the planner.
	dealOn := func(bench string) []string {
		t.Helper()
		token := "1.0123456789abcdef0123456789abcdef"
		sum := sha256.Sum256([]byte(token))
		reply, err := client.FCall(ctx, "ns_card_deal", nil, bench, "fence", "ctl", "",
			sprint, f.label, "1", token, hex.EncodeToString(sum[:])[:12]).StringSlice()
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if reply := dealOn("vision"); len(reply) != 1 || reply[0] != "DEALT" {
		t.Fatalf("ns_card_deal on vision = %v, want DEALT with no cards", reply)
	}
	if reply := dealOn("hulk"); len(reply) != 5 || reply[0] != "DEALT" || reply[2] != f.label {
		t.Fatalf("ns_card_deal on hulk = %v, want the pinned card dealt", reply)
	}
	c, err := client.HMGet(ctx, keyCard(f.label), "state", "bench", "pin").Result()
	if err != nil || c[0] != "dealt" || c[1] != "hulk" || c[2] != "hulk" {
		t.Fatalf("card after deal %v (%v), want dealt on hulk with pin hulk", c, err)
	}
}

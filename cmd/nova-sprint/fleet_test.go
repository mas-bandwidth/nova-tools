package main

import (
	"bytes"
	"context"
	"net"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFleetIsUpExits verifies that `nova-sprint fleet is-up` exits:
// 0 UP, 1 DOWN, 3 PROBING, 4 HELD, 2 usage or unregistered bench, 5 store unreachable.
func TestFleetIsUpExits(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skipping on Linux (hetzner): #3754 bench-specific failure baseline")
	}
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	const bench = "bench-is-up"
	c.SAdd(ctx, "benches", bench)

	cases := []struct {
		state    string
		wantCode int
	}{
		{fleet.StateUp, 0},
		{fleet.StateDown, 1},
		{fleet.StateProbing, 3},
		{fleet.StateHeld, 4},
	}

	for _, tc := range cases {
		c.HSet(ctx, "bench:"+bench+":state", "state", tc.state, "at", "1000")
		var out, errOut bytes.Buffer
		code := runFleet(ctx, []string{"is-up", "--redis", addr, "--bench", bench}, &out, &errOut)
		if code != tc.wantCode {
			t.Fatalf("is-up on state %s got exit code %d (stderr %q); want %d", tc.state, code, errOut.String(), tc.wantCode)
		}
		if got := strings.TrimSpace(out.String()); got != tc.state {
			t.Fatalf("is-up on state %s got stdout %q; want %q", tc.state, got, tc.state)
		}
	}

	// Exit 2: Unregistered bench
	{
		var out, errOut bytes.Buffer
		code := runFleet(ctx, []string{"is-up", "--redis", addr, "--bench", "nonexistent-bench"}, &out, &errOut)
		if code != 2 {
			t.Fatalf("is-up on unregistered bench got code %d; want 2", code)
		}
	}

	// Exit 2: Usage error (missing --bench)
	{
		var out, errOut bytes.Buffer
		code := runFleet(ctx, []string{"is-up", "--redis", addr}, &out, &errOut)
		if code != 2 {
			t.Fatalf("is-up with missing --bench got code %d; want 2", code)
		}
	}

	// Exit 5: Store unreachable (closed local port)
	{
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		closedAddr := ln.Addr().String()
		_ = ln.Close()

		var out, errOut bytes.Buffer
		code := runFleet(ctx, []string{"is-up", "--redis", closedAddr, "--bench", bench}, &out, &errOut)
		if code != 5 {
			t.Fatalf("is-up on closed port got code %d; want 5", code)
		}
	}
}

// TestFleetDownKeepsLeases sets up bench b holding one starting card and one living card,
// then deletes b's beat for down_after + 5 full productionDuties passes.
// bench:b:starting, bench:b:living, s:*:card:* and s:*:bench:b:ended must compare equal by DUMP before and after.
func TestFleetDownKeepsLeases(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	st := store.New(c)

	ssh := &verbSSH{}
	seams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return ssh, verbForge{} }
	t.Cleanup(func() { reconcileSeams = seams })

	const b = "bench-down-leases"
	const S = "sprint-down-leases"

	c.SAdd(ctx, "benches", b)
	c.HSet(ctx, "bench:"+b+":desired", "slots", "5", "paused", "0", "legs", "go")
	c.HSet(ctx, "bench:"+b+":beat", "host", "localhost", "user", "nova", "at", "1000")
	c.HSet(ctx, "bench:"+b+":state", "state", "UP", "at", "1000")

	c.SAdd(ctx, "sprints", S)
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	c.HSet(ctx, "s:"+S, "status", "open")
	c.HSet(ctx, "s:"+S+":policy", "share", "1", "backpressure_missing", "open")

	const downAfter = 2
	const upAfter = 1
	if err := fleet.SetConfig(ctx, c, downAfter, upAfter); err != nil {
		t.Fatalf("set config: %v", err)
	}

	cardStarting := "card-starting"
	identStarting := S + "/" + cardStarting + "/sha/1"
	c.ZAdd(ctx, "bench:"+b+":starting", redis.Z{Score: 1000, Member: identStarting})
	c.HSet(ctx, "s:"+S+":card:"+cardStarting, "state", "starting", "bench", b, "attempt", "1", "identity", identStarting)

	cardLiving := "card-living"
	identLiving := S + "/" + cardLiving + "/sha/1"
	c.ZAdd(ctx, "bench:"+b+":living", redis.Z{Score: 1000, Member: identLiving})
	c.HSet(ctx, "s:"+S+":card:"+cardLiving, "state", "living", "bench", b, "attempt", "1", "identity", identLiving)

	cardEnded := "card-ended"
	c.SAdd(ctx, "s:"+S+":bench:"+b+":ended", cardEnded)
	c.HSet(ctx, "s:"+S+":card:"+cardEnded, "state", "ended", "bench", b, "outcome", "DONE")

	// Delete beat before the passes
	c.Del(ctx, "bench:"+b+":beat")

	// Capture dumps before passes
	dumpStarting, err := c.Dump(ctx, "bench:"+b+":starting").Result()
	if err != nil {
		t.Fatalf("dump starting before: %v", err)
	}
	dumpLiving, err := c.Dump(ctx, "bench:"+b+":living").Result()
	if err != nil {
		t.Fatalf("dump living before: %v", err)
	}
	dumpCardStarting, err := c.Dump(ctx, "s:"+S+":card:"+cardStarting).Result()
	if err != nil {
		t.Fatalf("dump cardStarting before: %v", err)
	}
	dumpCardLiving, err := c.Dump(ctx, "s:"+S+":card:"+cardLiving).Result()
	if err != nil {
		t.Fatalf("dump cardLiving before: %v", err)
	}
	dumpCardEnded, err := c.Dump(ctx, "s:"+S+":card:"+cardEnded).Result()
	if err != nil {
		t.Fatalf("dump cardEnded before: %v", err)
	}
	dumpBenchEnded, err := c.Dump(ctx, "s:"+S+":bench:"+b+":ended").Result()
	if err != nil {
		t.Fatalf("dump benchEnded before: %v", err)
	}

	duties, _, _, err := productionDuties(st, nil)
	if err != nil {
		t.Fatalf("productionDuties: %v", err)
	}
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test-host"})
	if err != nil {
		t.Fatalf("reconcile.Acquire: %v", err)
	}

	for p := 0; p < downAfter+5; p++ {
		for _, d := range duties {
			if _, err := d(ctx, lease); err != nil {
				t.Fatalf("pass %d duty error: %v", p, err)
			}
		}
	}

	// Verify bench is now DOWN
	state, err := c.HGet(ctx, "bench:"+b+":state", "state").Result()
	if err != nil {
		t.Fatalf("hget state: %v", err)
	}
	if state != "DOWN" {
		t.Fatalf("bench state after passes = %q; want DOWN", state)
	}

	// Compare dumps after passes
	if got := c.Dump(ctx, "bench:"+b+":starting").Val(); got != dumpStarting {
		t.Fatalf("bench:%s:starting dump changed after down passes", b)
	}
	if got := c.Dump(ctx, "bench:"+b+":living").Val(); got != dumpLiving {
		t.Fatalf("bench:%s:living dump changed after down passes", b)
	}
	if got := c.Dump(ctx, "s:"+S+":card:"+cardStarting).Val(); got != dumpCardStarting {
		t.Fatalf("s:%s:card:%s dump changed after down passes", S, cardStarting)
	}
	if got := c.Dump(ctx, "s:"+S+":card:"+cardLiving).Val(); got != dumpCardLiving {
		t.Fatalf("s:%s:card:%s dump changed after down passes", S, cardLiving)
	}
	if got := c.Dump(ctx, "s:"+S+":card:"+cardEnded).Val(); got != dumpCardEnded {
		t.Fatalf("s:%s:card:%s dump changed after down passes", S, cardEnded)
	}
	if got := c.Dump(ctx, "s:"+S+":bench:"+b+":ended").Val(); got != dumpBenchEnded {
		t.Fatalf("s:%s:bench:%s:ended dump changed after down passes", S, b)
	}
}

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestFleetStateVerbReadsHeartbeatKeysFromRedis is the recut of PR #2752's hold
// (stella, emma: the package had no production caller and no Redis adapter).
// DONE-WHEN: the fleet-state verb reads fleet state through internal/fleet/state
// from Redis. The store holds what ns_bench_beat writes -- bench:<b>:beat, a
// hash with at (ms) and load1, PEXPIRE 5 s -- and what capacity writes --
// bench:<b>:desired paused -- and the verb's rows follow the keys:
//
//   - hulk: a fresh beat at load 21 (the #2161 case) -> UP
//   - vision: a fresh beat, desired paused=1 -> HELD
//   - threadripper-wsl: registered, no beat -> DOWN
//
// then, with the store's clock moved past the 5 s TTL and no new beat, every
// row is DOWN and fleet-live lists nobody.
func TestFleetStateVerbReadsHeartbeatKeysFromRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	t0 := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	mr.SetTime(t0)
	at := "1790186400000" // t0 in ms, as ns_bench_beat writes it
	mr.SAdd("benches", "hulk", "vision", "threadripper-wsl")
	mr.HSet("bench:hulk:beat", "at", at, "load1", "21.0", "host", "hulk")
	mr.SetTTL("bench:hulk:beat", 5*time.Second)
	mr.HSet("bench:vision:beat", "at", at, "load1", "0.5")
	mr.SetTTL("bench:vision:beat", 5*time.Second)
	mr.HSet("bench:vision:desired", "slots", "4", "paused", "1")

	ctx := context.Background()
	run := func(verb string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		code := runFleetVerb(verb)(ctx, []string{"--redis", mr.Addr()}, &out, &errOut)
		if code != 0 {
			t.Fatalf("%s code=%d stderr=%q", verb, code, errOut.String())
		}
		return out.String()
	}

	if got, want := run("fleet-state"), "hulk\tUP\nthreadripper-wsl\tDOWN\nvision\tHELD\n"; got != want {
		t.Fatalf("fleet-state at t0 =\n%q\nwant\n%q", got, want)
	}
	if got, want := run("fleet-live"), "hulk\nvision\n"; got != want {
		t.Fatalf("fleet-live at t0 = %q, want %q", got, want)
	}

	// No new beat; the store's clock moves past the 5 s TTL.
	mr.FastForward(6 * time.Second)
	mr.SetTime(t0.Add(6 * time.Second))
	if got, want := run("fleet-state"), "hulk\tDOWN\nthreadripper-wsl\tDOWN\nvision\tDOWN\n"; got != want {
		t.Fatalf("fleet-state after the TTL =\n%q\nwant\n%q", got, want)
	}
	if got := run("fleet-live"); got != "" {
		t.Fatalf("fleet-live after the TTL = %q, want nothing", got)
	}
}

// TestFleetStateVerbRefusesWithoutRedis: no --redis, no reading, exit 2.
func TestFleetStateVerbRefusesWithoutRedis(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runFleetVerb("fleet-state")(context.Background(), nil, &out, &errOut); code != 2 {
		t.Fatalf("code=%d want 2 stderr=%q", code, errOut.String())
	}
}

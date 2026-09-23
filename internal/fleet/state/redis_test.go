package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/fleet/state"
)

// TestReadRedisDecidesByTheKeyAndItsTTL is the adapter's own check: the
// heartbeat key as ns_bench_beat writes it (bench:<b>:beat, at in ms, PEXPIRE)
// becomes a Key whose expiry is the store's TTL, load1 becomes Load and never a
// say, paused on bench:<b>:desired rides the key as the hold, and a bench with
// no beat has no Key. The store's clock is the decision's clock.
func TestReadRedisDecidesByTheKeyAndItsTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	t0 := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	mr.SetTime(t0)
	mr.SAdd(state.Registry, "space", "hulk", "freddy")
	// space beat 2 s ago with a 5 s key, at load 21: 3 s remain.
	mr.HSet(state.BeatKey("space"), "at", "1790186398000", "load1", "21")
	mr.SetTTL(state.BeatKey("space"), 3*time.Second)
	// hulk's beat has no TTL (a writer that forgot PEXPIRE) and is paused.
	mr.HSet(state.BeatKey("hulk"), "at", "garbage")
	mr.HSet(state.DesiredKey("hulk"), "paused", "true")

	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	benches, now, err := state.ReadRedis(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !now.Equal(t0) {
		t.Fatalf("now = %s, want the store's TIME %s", now, t0)
	}
	if len(benches) != 3 || benches[0].Name != "freddy" || benches[1].Name != "hulk" || benches[2].Name != "space" {
		t.Fatalf("benches = %+v, want freddy, hulk, space sorted", benches)
	}
	if benches[0].Key != nil || benches[0].State(now) != state.Down {
		t.Fatalf("freddy has no beat and reads %s with key %+v, want DOWN and no key", benches[0].State(now), benches[0].Key)
	}
	if got := benches[1].State(now); got != state.Held {
		t.Fatalf("hulk (paused, key present) = %s, want HELD", got)
	}
	if got := benches[1].State(now.Add(time.Second)); got != state.Down {
		t.Fatalf("hulk's untimed key counts only at the read, got %s a second later, want DOWN", got)
	}
	sp := benches[2]
	if sp.Load != 21 || sp.Key == nil {
		t.Fatalf("space = %+v, want load 21 and a key", sp)
	}
	if !sp.Key.Written.Equal(t0.Add(-2*time.Second)) || sp.Key.TTL != 5*time.Second {
		t.Fatalf("space key = %+v, want written t0-2s with TTL 5s", *sp.Key)
	}
	if got := sp.State(now.Add(2 * time.Second)); got != state.Up {
		t.Fatalf("space 2 s on = %s, want UP (load 21 is never a say)", got)
	}
	if got := sp.State(now.Add(3 * time.Second)); got != state.Down {
		t.Fatalf("space at its expiry = %s, want DOWN", got)
	}
}

// TestReadRedisEmptyFleet: no registered bench is no rows, not an error.
func TestReadRedisEmptyFleet(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	benches, _, err := state.ReadRedis(context.Background(), c)
	if err != nil || len(benches) != 0 {
		t.Fatalf("benches=%v err=%v, want none and no error", benches, err)
	}
}

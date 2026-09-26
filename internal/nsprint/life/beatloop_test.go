package life_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/redis/go-redis/v9"
)

// loopStore is an in-process store for the beat loop's lease.
func loopStore(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

// ticks is a fake beat: each call returns the next working count (the last
// one repeats) and records the clock it was given.
type ticks struct {
	n  []int
	at []time.Time
}

func (f *ticks) tick(_ context.Context, now time.Time) (int, error) {
	f.at = append(f.at, now)
	v := f.n[0]
	if len(f.n) > 1 {
		f.n = f.n[1:]
	}
	return v, nil
}

// TestBeatLoopIdleTwoTicksReleases (seat-keeps-beat, invariant A): a loop
// renews its lease each step and keeps it while the friend holds working
// copies; two ticks in a row with none release the lease and end it IDLE.
func TestBeatLoopIdleTwoTicksReleases(t *testing.T) {
	t.Parallel()
	mr, c := loopStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	if ok, err := life.ClaimBeatLoop(ctx, c, "rowan", "tok"); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	f := &ticks{n: []int{2, 0, 0, 0}}
	l := &life.BeatLoop{Client: c, Friend: "rowan", Token: "tok", Tick: f.tick}
	for i, want := range []bool{false, false} {
		mr.FastForward(5 * time.Second) // under the lease: each step renews it
		done, why, err := l.Step(ctx, now.Add(time.Duration(i)*time.Second))
		if err != nil || done != want {
			t.Fatalf("step %d: done=%v why=%q err=%v", i, done, why, err)
		}
		if got, _ := mr.Get(life.BeatLoopKey("rowan")); got != "tok" || mr.TTL(life.BeatLoopKey("rowan")) != life.BeatLoopLease {
			t.Fatalf("step %d: lease %q ttl %s", i, got, mr.TTL(life.BeatLoopKey("rowan")))
		}
	}
	done, why, err := l.Step(ctx, now.Add(2*time.Second))
	if err != nil || !done || !strings.HasPrefix(why, "IDLE ") {
		t.Fatalf("second idle tick: done=%v why=%q err=%v", done, why, err)
	}
	if mr.Exists(life.BeatLoopKey("rowan")) {
		t.Fatal("an idle loop kept its lease")
	}
	if len(f.at) != 4 || !f.at[3].Equal(now.Add(2*time.Second)) {
		t.Fatalf("ticks at %v", f.at)
	}
}

// TestBeatLoopHandsOverAndLoses: a copy worked as the loop lets go is seen
// by its last tick and the loop takes the lease back; a lease another loop
// holds ends this one LOST, untouched.
func TestBeatLoopHandsOverAndLoses(t *testing.T) {
	t.Parallel()
	mr, c := loopStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	f := &ticks{n: []int{0, 0, 1, 1}}
	l := &life.BeatLoop{Client: c, Friend: "rowan", Token: "tok", Tick: f.tick}
	for i := 0; i < 3; i++ {
		if done, why, err := l.Step(ctx, now); done || err != nil {
			t.Fatalf("step %d: done=%v why=%q err=%v", i, done, why, err)
		}
	}
	if got, _ := mr.Get(life.BeatLoopKey("rowan")); got != "tok" {
		t.Fatalf("after a late copy the lease is %q, want tok", got)
	}
	mr.Set(life.BeatLoopKey("rowan"), "other")
	done, why, err := l.Step(ctx, now)
	if err != nil || !done || !strings.HasPrefix(why, "LOST ") {
		t.Fatalf("another loop's lease: done=%v why=%q err=%v", done, why, err)
	}
	if got, _ := mr.Get(life.BeatLoopKey("rowan")); got != "other" {
		t.Fatalf("a lost loop touched the lease: %q", got)
	}
}

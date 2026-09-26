package life_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// fakeLease is a beat loop lease in memory: holder is the token holding it
// ("" free); mine is this loop's token.
type fakeLease struct {
	holder, mine string
	renews       int
}

func (f *fakeLease) Renew(context.Context) (bool, error) {
	if f.holder != "" && f.holder != f.mine {
		return false, nil
	}
	f.holder = f.mine
	f.renews++
	return true, nil
}

func (f *fakeLease) Release(context.Context) error {
	if f.holder == f.mine {
		f.holder = ""
	}
	return nil
}

func (f *fakeLease) Claim(context.Context) (bool, error) {
	if f.holder != "" {
		return false, nil
	}
	f.holder = f.mine
	return true, nil
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
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	lease := &fakeLease{holder: "tok", mine: "tok"}
	f := &ticks{n: []int{2, 0, 0, 0}}
	l := &life.BeatLoop{Lease: lease, Friend: "rowan", Tick: f.tick}
	for i := 0; i < 2; i++ {
		done, why, err := l.Step(ctx, now.Add(time.Duration(i)*time.Second))
		if err != nil || done || lease.holder != "tok" || lease.renews != i+1 {
			t.Fatalf("step %d: done=%v why=%q err=%v, lease %q renewed %d", i, done, why, err, lease.holder, lease.renews)
		}
	}
	done, why, err := l.Step(ctx, now.Add(2*time.Second))
	if err != nil || !done || !strings.HasPrefix(why, "IDLE ") {
		t.Fatalf("second idle tick: done=%v why=%q err=%v", done, why, err)
	}
	if lease.holder != "" {
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
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	lease := &fakeLease{mine: "tok"}
	f := &ticks{n: []int{0, 0, 1, 1}}
	l := &life.BeatLoop{Lease: lease, Friend: "rowan", Tick: f.tick}
	for i := 0; i < 3; i++ {
		if done, why, err := l.Step(ctx, now); done || err != nil {
			t.Fatalf("step %d: done=%v why=%q err=%v", i, done, why, err)
		}
	}
	if lease.holder != "tok" {
		t.Fatalf("after a late copy the lease is %q, want tok", lease.holder)
	}
	lease.holder = "other"
	done, why, err := l.Step(ctx, now)
	if err != nil || !done || !strings.HasPrefix(why, "LOST ") {
		t.Fatalf("another loop's lease: done=%v why=%q err=%v", done, why, err)
	}
	if lease.holder != "other" {
		t.Fatalf("a lost loop touched the lease: %q", lease.holder)
	}
}

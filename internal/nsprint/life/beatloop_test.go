package life_test

import (
	"context"
	"errors"
	"slices"
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

// clockLease is a lease whose renewals fail while its clock is before
// failUntil, recording the clock of each renewal that held.
type clockLease struct {
	fakeLease
	now       *time.Time
	failUntil time.Time
	held      []time.Time
}

func (f *clockLease) Renew(ctx context.Context) (bool, error) {
	if f.now.Before(f.failUntil) {
		return false, errors.New("store down")
	}
	ok, err := f.fakeLease.Renew(ctx)
	if ok {
		f.held = append(f.held, *f.now)
	}
	return ok, err
}

// TestBeatLoopTickErrorsKeepLease (seat-beat-fix3, probe b): a loop whose
// tick fails for 20 s (the store refusing the copies' renewal) backs its
// tick off 1, 2, 4, 4 ... s (never past BeatLoopMaxBackoff, which is under
// BeatLoopLease) and renews its lease on every one-second step all along,
// so the lease never goes a second unrenewed while the loop is alive; once
// the tick succeeds the backoff is 0 and the tick runs every step again.
func TestBeatLoopTickErrorsKeepLease(t *testing.T) {
	t.Parallel()
	if BeatLoopMaxBackoff := life.BeatLoopMaxBackoff; BeatLoopMaxBackoff >= life.BeatLoopLease {
		t.Fatalf("BeatLoopMaxBackoff %s is not under BeatLoopLease %s", BeatLoopMaxBackoff, life.BeatLoopLease)
	}
	ctx := context.Background()
	t0 := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	now := t0
	lease := &clockLease{fakeLease: fakeLease{holder: "tok", mine: "tok"}, now: &now}
	var tried []time.Duration
	l := &life.BeatLoop{Lease: lease, Friend: "probe", Tick: func(_ context.Context, at time.Time) (int, error) {
		tried = append(tried, at.Sub(t0))
		if at.Sub(t0) < 20*time.Second {
			return 0, errors.New("REFUSED NOTWORKING fq-probe-1 is not in friend:probe:cards:working")
		}
		return 2, nil
	}}
	var backoffs []time.Duration
	for i := 0; i <= 25; i++ {
		now = t0.Add(time.Duration(i) * time.Second)
		done, why, err := l.Step(ctx, now)
		if done {
			t.Fatalf("step %d: done %q", i, why)
		}
		if err != nil {
			backoffs = append(backoffs, l.RetryIn())
		}
	}
	if len(lease.held) != 26 || lease.holder != "tok" {
		t.Fatalf("renewed %d of 26 steps (%v), holder %q", len(lease.held), lease.held, lease.holder)
	}
	want := []time.Duration{0, 1, 3, 7, 11, 15, 19, 23, 24, 25}
	for i := range want {
		want[i] *= time.Second
	}
	if !slices.Equal(tried, want) {
		t.Fatalf("ticks at %v, want %v", tried, want)
	}
	if !slices.Equal(backoffs, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}) {
		t.Fatalf("backoffs %v", backoffs)
	}
	if l.RetryIn() != 0 {
		t.Fatalf("after a good tick RetryIn = %s", l.RetryIn())
	}
}

// TestBeatLoopRenewErrorsBackOffUnderLease (seat-beat-fix3): a loop whose
// own renewals fail (the store down for 20 s) backs off 1, 2, 4, 4 ... s
// and neither renews nor ticks while it waits; once the store is back it
// renews within BeatLoopMaxBackoff, under BeatLoopLease.
func TestBeatLoopRenewErrorsBackOffUnderLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	now := t0
	lease := &clockLease{fakeLease: fakeLease{holder: "tok", mine: "tok"}, now: &now, failUntil: t0.Add(20 * time.Second)}
	f := &ticks{n: []int{1}}
	l := &life.BeatLoop{Lease: lease, Friend: "probe", Tick: f.tick}
	var tried []time.Duration
	for i := 0; i <= 25; i++ {
		now = t0.Add(time.Duration(i) * time.Second)
		if _, _, err := l.Step(ctx, now); err != nil {
			tried = append(tried, now.Sub(t0))
		}
	}
	if !slices.Equal(tried, []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second, 11 * time.Second, 15 * time.Second, 19 * time.Second}) {
		t.Fatalf("renewals tried at %v", tried)
	}
	if len(lease.held) == 0 || lease.held[0].Sub(t0) != 23*time.Second || len(f.at) != len(lease.held) {
		t.Fatalf("renewed at %v, ticked at %v", lease.held, f.at)
	}
	if gap := lease.held[0].Sub(t0.Add(20 * time.Second)); gap > life.BeatLoopMaxBackoff {
		t.Fatalf("store back at 20 s, renewed %s later", gap)
	}
}

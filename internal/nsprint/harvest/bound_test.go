package harvest_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// fakeBound is the reconciler lease a pass runs under (#3737).
type fakeBound struct {
	left   atomic.Int64 // nanoseconds
	fenced atomic.Bool
}

func (b *fakeBound) Remaining() time.Duration { return time.Duration(b.left.Load()) }
func (b *fakeBound) Fenced() bool             { return b.fenced.Load() }

// hookPusher runs after each successful push.
type hookPusher struct {
	fixturePusher
	after func()
}

func (p *hookPusher) Push(ctx context.Context, b harvest.BenchInfo, c harvest.Card) error {
	err := p.fixturePusher.Push(ctx, b, c)
	if err == nil && p.after != nil {
		p.after()
	}
	return err
}

// forgeFor is the fixture forge with each label's branch head as pushed.
func forgeFor(labels ...string) *fixtureForge {
	f := newForge()
	for _, l := range labels {
		f.heads["nova/"+sprint+"/"+l+"-a1"] = sha(l)
	}
	return f
}

// benchUp marks the bench UP with a beat, as ns_harvest_due requires.
func benchUp(t *testing.T, c *redis.Client, bench string) {
	t.Helper()
	ctx := context.Background()
	if err := c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "bench:"+bench+":beat", "host", bench+".fixture", "user", "nova", "at", "1").Err(); err != nil {
		t.Fatal(err)
	}
}

// TestHarvestBoundedByReconcilerLease is nova-tools #3737: a harvest pass
// under the reconciler lease starts no card with less than the write margin
// of it left, and a pass whose lease is fenced starts no further card; each
// records what it did in proc:harvest:<b> (n, took_ms, left=<k>, err=
// LEASE-MARGIN or err=FENCED), never a silent n=0, and gives back
// lease:harvest:<b> in the same call.
func TestHarvestBoundedByReconcilerLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	labels := []string{"ctl-hb-card1", "ctl-hb-card2", "ctl-hb-card3"}

	t.Run("margin", func(t *testing.T) {
		c := startRedis(t)
		st := store.New(c)
		benchUp(t, c, "ctl-a")
		for _, l := range labels {
			seedEnded(t, c, "ctl-a", l, "model", "DONE", sha(l))
		}
		b := &fakeBound{}
		b.left.Store(int64(300 * time.Millisecond))
		pusher := &fixturePusher{pushes: map[string]int{}}
		res := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"}, Clock: 10 * time.Second,
			Instance: "reconciler-x", Forge: newForge(), Pusher: pusher, Bound: b, Margin: time.Second})[0]
		if !errors.Is(res.Err, harvest.ErrBoundMargin) || res.Left != 3 || len(res.Cards) != 0 || len(pusher.pushes) != 0 {
			t.Fatalf("margin pass = %+v pushes %v; want LEASE-MARGIN, left 3, nothing started", res, pusher.pushes)
		}
		h := c.HGetAll(ctx, "proc:harvest:ctl-a").Val()
		if h["n"] != "0" || h["left"] != "3" || !strings.HasPrefix(h["err"], "LEASE-MARGIN") || h["took_ms"] == "" || h["holder"] != "" {
			t.Fatalf("proc:harvest:ctl-a = %v; want n=0 left=3 err=LEASE-MARGIN..., took_ms, holder cleared", h)
		}
		if c.Exists(ctx, "lease:harvest:ctl-a").Val() != 0 {
			t.Fatal("lease:harvest:ctl-a still held after the bounded pass")
		}
	})

	t.Run("fenced mid-pass", func(t *testing.T) {
		c := startRedis(t)
		st := store.New(c)
		benchUp(t, c, "ctl-a")
		for _, l := range labels {
			seedEnded(t, c, "ctl-a", l, "model", "DONE", sha(l))
		}
		b := &fakeBound{}
		b.left.Store(int64(time.Hour))
		// The reconciler lease is lost while the first card is in flight.
		pusher := &hookPusher{fixturePusher: fixturePusher{pushes: map[string]int{}}, after: func() { b.fenced.Store(true) }}
		res := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"}, Clock: 10 * time.Second,
			Instance: "reconciler-x", Forge: forgeFor(labels...), Pusher: pusher, Bound: b})[0]
		if !errors.Is(res.Err, harvest.ErrBoundFenced) || len(res.Cards) != 1 || res.Left != 2 {
			t.Fatalf("fenced pass = %+v; want FENCED, the in-flight card harvested, 2 left", res)
		}
		h := c.HGetAll(ctx, "proc:harvest:ctl-a").Val()
		if h["n"] != "1" || h["left"] != "2" || h["err"] != "FENCED" {
			t.Fatalf("proc:harvest:ctl-a = %v; want n=1 left=2 err=FENCED", h)
		}
		if c.Exists(ctx, "lease:harvest:ctl-a").Val() != 0 {
			t.Fatal("lease:harvest:ctl-a still held after the fenced pass")
		}
	})
}

// TestHarvestStaleLeaseTakenAndReleased (#3737): a worker that died holding
// lease:harvest:<b> leaves its name in proc:harvest:<b>; once the lease lapses
// the next worker takes it, is told the holder and logs `TAKEN from=<instance>
// stale`. Release gives a lease back by its token only.
func TestHarvestStaleLeaseTakenAndReleased(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := startRedis(t)
	st := store.New(c)
	benchUp(t, c, "ctl-a")
	seedEnded(t, c, "ctl-a", "ctl-st-card1", "model", "DONE", sha("ctl-st-card1"))

	died := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"}, Clock: 10 * time.Second,
		Instance: "reconciler-dead", Forge: newForge(), Pusher: &fixturePusher{pushes: map[string]int{}},
		Fault: func(step string) error {
			if step == harvest.FaultAfterPush {
				return errors.New("process died")
			}
			return nil
		}})[0]
	if died.Err == nil || c.HGet(ctx, "proc:harvest:ctl-a", "holder").Val() != "reconciler-dead" {
		t.Fatalf("dead worker = %+v, holder %q; want the fault and its name kept", died, c.HGet(ctx, "proc:harvest:ctl-a", "holder").Val())
	}
	c.Del(ctx, "lease:harvest:ctl-a") // its TTL lapses

	var mu sync.Mutex
	var lines []string
	next := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"}, Clock: 10 * time.Second,
		Instance: "reconciler-next", Forge: forgeFor("ctl-st-card1"), Pusher: &fixturePusher{pushes: map[string]int{}},
		Log: func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }})[0]
	if next.Err != nil || next.StaleFrom != "reconciler-dead" || len(next.Cards) != 1 {
		t.Fatalf("next worker = %+v; want the card harvested from a stale take of reconciler-dead", next)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "TAKEN from=reconciler-dead stale") {
		t.Fatalf("log %q; want one `TAKEN from=reconciler-dead stale` line", lines)
	}
	if h := c.HGetAll(ctx, "proc:harvest:ctl-a").Val(); h["stale_from"] != "reconciler-dead" || h["holder"] != "" {
		t.Fatalf("proc:harvest:ctl-a = %v; want stale_from reconciler-dead and the holder cleared by the pass", h)
	}

	// Release: only the holder's token gives the lease back.
	if r := c.FCall(ctx, harvest.FunctionLease, nil, "ctl-a", "holder", "tok", 60000).Val(); r != "TAKEN" {
		t.Fatalf("lease = %v, want TAKEN (the last holder released)", r)
	}
	if r, err := harvest.Release(ctx, st, "ctl-a", "holder", "stolen"); err != nil || r != "FENCED" || c.Exists(ctx, "lease:harvest:ctl-a").Val() != 1 {
		t.Fatalf("release with another token = %q %v; want FENCED and the lease kept", r, err)
	}
	if r, err := harvest.Release(ctx, st, "ctl-a", "holder", "tok"); err != nil || r != "RELEASED" || c.Exists(ctx, "lease:harvest:ctl-a").Val() != 0 {
		t.Fatalf("release by the holder = %q %v; want RELEASED and the lease gone", r, err)
	}
	if h := c.HGet(ctx, "proc:harvest:ctl-a", "holder").Val(); h != "" {
		t.Fatalf("holder %q after the release, want cleared", h)
	}
	if r := c.FCall(ctx, harvest.FunctionLease, nil, "ctl-a", "other", "tok2", 60000).Val(); r != "TAKEN" {
		t.Fatalf("take after a release = %v, want TAKEN (not stale)", r)
	}
}

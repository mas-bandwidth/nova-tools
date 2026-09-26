//go:build functional

package preflight

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestLiveBeatShapeNamesLauncher is #3191 on a throwaway redis-server with the
// real nova_sprint library: the seven live benches beat through the one beat
// writer (life.BenchBeat) exactly as `nova-sprint bench beat` calls it, with
// no --launcher given; preflight reads the hashes the writer left
// (GatherFleet) and 7.4 is GREEN. The same hash with the launcher field
// dropped is RED naming the bench.
func TestLiveBeatShapeNamesLauncher(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load %s: %v", fn.Library, err)
	}
	st := store.New(c)
	benches := []string{"studio", "hulk", "vision", "superman", "batman", "spacegame", "hetzner"}
	for _, b := range benches {
		if err := c.SAdd(ctx, "benches", b).Err(); err != nil {
			t.Fatal(err)
		}
		// The request runBenchBeat builds when no --launcher is passed.
		req := life.BenchRequest{Bench: b, Host: b, Session: "sess-" + b, Actor: "bench",
			Load1: "1.66", RowAt: time.Now(), NCPU: 16, TTL: time.Minute}
		if res, err := life.BenchBeat(ctx, st, req); err != nil || !res.Accepted {
			t.Fatalf("beat %s: %+v %v", b, res, err)
		}
	}
	if got := c.HGet(ctx, "bench:hetzner:beat", "launcher").Val(); got != BatchLauncherName {
		t.Fatalf("the beat writer published launcher=%q, want %q", got, BatchLauncherName)
	}

	f, err := GatherFleet(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	green := CheckBatchLauncher(f.Input())
	if green.Red || !strings.Contains(green.What, "7 beats name "+BatchLauncherName) {
		t.Fatalf("the writer's beats are not 7.4 GREEN: %s", green)
	}

	if err := c.HDel(ctx, "bench:hetzner:beat", "launcher").Err(); err != nil {
		t.Fatal(err)
	}
	f, err = GatherFleet(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	red := CheckBatchLauncher(f.Input())
	if !red.Red || !strings.Contains(red.What, "hetzner launcher MISSING") || strings.Contains(red.What, "studio") {
		t.Fatalf("a beat without the launcher field is not RED naming the bench: %s", red)
	}
}

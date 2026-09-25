package deal

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func throwawayRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestRedisSourceReadsTheSpecKeys reads the #2756 2.2/2.3 keys in pipelined
// rounds: free is desired minus starting+living, a bench with no beat is down,
// only open sprints in sprint:order deal, only queued pool cards are read, and
// a missing backpressure hash applies the declared policy.
func TestRedisSourceReadsTheSpecKeys(t *testing.T) {
	t.Parallel()

	c := throwawayRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "ctl-a", "ctl-down")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "8", "legs", "go,lua")
	c.HSet(ctx, "bench:ctl-a:beat", "host", "ctl-a.tail", "user", "bench")
	c.HSet(ctx, "bench:ctl-a:state", "state", "UP", "at", "1")
	c.ZAdd(ctx, "bench:ctl-a:starting", redis.Z{Score: 1, Member: "x/1"})
	c.ZAdd(ctx, "bench:ctl-a:living", redis.Z{Score: 1, Member: "x/2"}, redis.Z{Score: 1, Member: "x/3"})
	c.HSet(ctx, RowKey("ctl-a"), "state", "refused", "at", "1700000000000")
	c.HSet(ctx, "bench:ctl-down:desired", "slots", "8")
	c.SAdd(ctx, "sprints", "control-a", "control-b")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "control-a"}, redis.Z{Score: 2, Member: "control-b"}, redis.Z{Score: 3, Member: "control-gone"})
	c.HSet(ctx, "s:control-a", "status", "open")
	c.HSet(ctx, "s:control-a:policy", "share", "3", "backpressure_missing", "closed")
	c.ZAdd(ctx, "s:control-a:pool", redis.Z{Score: 2, Member: "one"}, redis.Z{Score: -1, Member: "front"}, redis.Z{Score: 5, Member: "dealt"})
	c.HSet(ctx, "s:control-a:card:one", "state", "queued", "leg", "go")
	c.HSet(ctx, "s:control-a:card:front", "state", "queued", "tier", "priority", "bench", "ctl-a")
	c.HSet(ctx, "s:control-a:card:dealt", "state", "dealt")
	c.HSet(ctx, "s:control-b", "status", "paused")

	in, err := RedisSource{Client: c}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Benches) != 2 {
		t.Fatalf("benches %+v", in.Benches)
	}
	a, down := in.Benches[0], in.Benches[1]
	if !a.Up || a.Free() != 5 || a.Target() != "bench@ctl-a.tail" || strings.Join(a.Legs, ",") != "go,lua" || a.SSH != SSHRefused || a.SSHAt.UnixMilli() != 1700000000000 {
		t.Fatalf("bench ctl-a read as %+v", a)
	}
	if down.Up {
		t.Fatalf("a bench with no beat read as up")
	}
	if len(in.Sprints) != 1 || in.Sprints[0].Name != "control-a" || in.Sprints[0].Share != 3 || !in.Sprints[0].Backpressure {
		t.Fatalf("sprints %+v", in.Sprints)
	}
	pool := in.Sprints[0].Pool
	if len(pool) != 2 || pool[0].Label != "front" || pool[0].Tier != TierPriority || pool[0].Bench != "ctl-a" || pool[1].Leg != "go" {
		t.Fatalf("pool %+v", pool)
	}
	if in.Now.IsZero() {
		t.Fatal("no Redis server time")
	}
}

package deal

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// TestPlanSkipsOnlyThePitStoppedSprint: a stop on one sprint leaves the
// other open sprints' cards flowing to the whole fleet.
func TestPlanSkipsOnlyThePitStoppedSprint(t *testing.T) {
	t.Parallel()

	in := Input{Now: time.Now(), Benches: []Bench{{Name: "b", Up: true, Slots: 4}}, Sprints: []Sprint{
		{Name: "stopped", Share: 1, Pitstop: true, Pool: poolCards("stopped", 4)},
		{Name: "running", Share: 1, Pool: poolCards("running", 2)},
	}}
	plan := Plan(in, DefaultRefusedHold)
	if len(plan) != 1 || len(plan[0].Cards) != 2 {
		t.Fatalf("plan = %+v, want the 2 running cards only", plan)
	}
	for _, c := range plan[0].Cards {
		if c.Sprint != "running" {
			t.Fatalf("planned %s/%s from the pit-stopped sprint", c.Sprint, c.Label)
		}
	}
}

// TestRedisSourceReadsPitstopOnMiniredis reads the key with the plain
// commands RedisSource pipelines (no function needed): present is stopped,
// absent is not.
func TestRedisSourceReadsPitstopOnMiniredis(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	for _, s := range []string{"a", "b"} {
		c.SAdd(ctx, "sprints", s)
		c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: s})
		c.HSet(ctx, "s:"+s, "status", "open")
	}
	c.HSet(ctx, pitstop.Key("a"), "by", "rowan", "why", "x", "at", "1")
	in, err := RedisSource{Client: c}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range in.Sprints {
		got[s.Name] = s.Pitstop
	}
	if len(got) != 2 || !got["a"] || got["b"] {
		t.Fatalf("pitstop by sprint = %v, want a stopped and b not", got)
	}
}

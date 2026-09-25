package deal

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// TestPitstopDealPassPlansNothingUntilCleared is the dealer half of the
// DONE-WHEN of nova-tools #3371: with s:<S>:pitstop set, a deal pass plans
// nothing, reserves nothing and opens no session; ns_card_deal called
// directly (a dealer that skipped the plan) deals nothing from the sprint;
// cleared, the same pass deals the pool.
func TestPitstopDealPassPlansNothingUntilCleared(t *testing.T) {
	const sprint = "control-00003371"
	ctx := context.Background()
	c := dealRedis(t)
	seedFleet(t, c, sprint, 8, map[string]int{"ctl-a": 8})
	seedLease(t, c, "lease-1")
	if r, err := pitstop.Set(ctx, c, sprint, "rowan", "control", false, ""); err != nil || r.Outcome != pitstop.Done {
		t.Fatalf("set: %+v %v", r, err)
	}

	in, err := RedisSource{Client: c}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Sprints) != 1 || !in.Sprints[0].Pitstop {
		t.Fatalf("source sprints = %+v, want the one sprint read as pit-stopped", in.Sprints)
	}
	if plan := Plan(in, DefaultRefusedHold); len(plan) != 0 {
		t.Fatalf("pit-stopped plan = %+v, want nothing", plan)
	}

	f := newFixture(t)
	st := newFnStore(c)
	p := &Pass{Source: RedisSource{Client: c}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 0 || res.Launched() != 0 || st.calls["ctl-a"] != 0 {
		t.Fatalf("pit-stopped pass: rounds %d launched %d reserve calls %d, want 0 0 0", res.Rounds, res.Launched(), st.calls["ctl-a"])
	}
	if n := len(f.lines("ctl-a", "sessions.log")); n != 0 {
		t.Fatalf("pit-stopped pass opened %d sessions", n)
	}
	if n := zcard(t, c, "s:"+sprint+":pool"); n != 8 {
		t.Fatalf("pool = %d, want 8 untouched", n)
	}

	// The function is the backstop: a reservation that skips the plan is refused.
	res2, err := st.Reserve(ctx, "lease-1", "ctl-a", poolCards(sprint, 2))
	if err != nil {
		t.Fatal(err)
	}
	if len(res2) != 0 || zcard(t, c, "bench:ctl-a:cards:working") != 0 {
		t.Fatalf("ns_card_deal on a pit-stopped sprint reserved %+v", res2)
	}
	if n := len(logEntries(t, c, sprint, "card deal", "")); n != 0 {
		t.Fatalf("pit-stopped sprint has %d card deal receipts", n)
	}

	if r, err := pitstop.Clear(ctx, c, sprint, "rowan", ""); err != nil || r.Outcome != pitstop.Done {
		t.Fatalf("clear: %+v %v", r, err)
	}
	res, err = p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Launched() != 8 {
		t.Fatalf("cleared pass launched %d, want 8 (%+v)", res.Launched(), res.Benches)
	}
	if n := zcard(t, c, "s:"+sprint+":pool"); n != 0 {
		t.Fatalf("pool = %d after the cleared pass, want 0", n)
	}
}

// TestPlanSkipsOnlyThePitStoppedSprint: a stop on one sprint leaves the
// other open sprints' cards flowing to the whole fleet.
func TestPlanSkipsOnlyThePitStoppedSprint(t *testing.T) {
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

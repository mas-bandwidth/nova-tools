//go:build functional

package store

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/redisacl"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A worker's steps run as the fleet's member user, under the ACL internal/redisacl
// renders for it, on a real store: take, finish (ok and failed, each with usage, so
// it prices with the routes) and read (ok, broken and return, each with usage). The
// fleet's finish was refused NOPERM on 2026-10-01 because it read the tiers' arrays;
// the coordinator's steps run as the store's own user, as on the fleet.
func TestRedisAWorkersStepsRunUnderTheRenderedMemberACL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	users, err := redisacl.Render(fn.Spec())
	require.NoError(t, err)
	const pw = "member-test-pw"
	var member string
	for _, u := range users {
		if u.Role != redisacl.Member {
			continue
		}
		member = u.Name
		args := []any{"ACL", "SETUSER", u.Name}
		for _, r := range u.Rules {
			args = append(args, r)
		}
		require.NoError(t, admin.Do(ctx, append(args, ">"+pw)...).Err())
	}
	require.NotEmpty(t, member, "the render has a member role")

	// the routes as nova-config's apply writes them, the tier's array among them
	require.NoError(t, admin.SAdd(ctx, config.RoutesKey, "flash-a").Err())
	require.NoError(t, admin.HSet(ctx, config.RouteKey("flash-a"), "name", "flash-a", "tier", "flash", "provider", "opencode", "model", "m",
		"tokens", "400000", "deadline", "900", "enabled", "true", "price_input", "0.14", "price_output", "0.28", "reasoning_as_output", "true").Err())
	require.NoError(t, admin.HSet(ctx, config.TierKey("flash"), "name", "flash", "routes", "flash-a").Err())

	names := sprint.Names{} // the fleet's own key names: no prefix
	coord := &Store{B: &Redis{C: admin, Names: names, Now: time.Now}, Names: names, Actor: "coordinator"}
	require.NoError(t, coord.Init(ctx))
	require.NoError(t, coord.B.RowsAdd(ctx, names.Table(sprint.Readers), []string{"reader-a", "reader-b", "reader-c"}))
	h := &harness{t: t, st: coord, ctx: ctx, now: time.Now(), live: []string{"m1"}}

	bench := redis.NewClient(&redis.Options{Addr: addr, Username: member, Password: pw})
	t.Cleanup(func() { _ = bench.Close() })
	w := &harness{t: t, st: &Store{B: &Redis{C: bench, Names: names, Now: time.Now}, Names: names, Actor: "m1"}, ctx: ctx, now: time.Now()}

	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 8}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 4, Brief: "c: the work (s1) tier: flash\n\nThe task.\n"}))
	h.must(DealStep(sprint.DealReq{}))
	usage := "wall=1.00s budget=11/400000 input=10 output=1 model=opencode/m actual_usd=0.001 actual_by=harness"
	ids := []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"}
	gens := map[string]int{}
	for _, id := range ids {
		gens[id] = h.snap().Fleet.Card(id).Int("gen")
	}
	w.must(TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: ids}, As: "m1", Gens: gens, Who: "m1"}))
	for _, id := range ids[:3] {
		w.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{id}}, As: "m1", Gens: map[string]int{id: gens[id]}, Report: "done", Usage: usage, Who: "m1"}))
	}
	w.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{ids[3]}}, As: "m1", Gens: map[string]int{ids[3]: gens[ids[3]]}, Failed: true,
		Report: "tests red", Usage: usage, Who: "m1"}))
	assert.Contains(t, h.snap().Fleet.Card(ids[0]).F(sprint.FieldUsage), "price_route=flash-a", "the finish priced the take with the route it may read")

	h.beat()
	h.must(AskStep(sprint.AskReq{}))
	// a flash card is read once (cost rule 4): s1-1's read says ok, s1-2's broken, s1-3's is returned
	reads := h.snap().Readers.Of("s1-1")
	require.Len(t, reads, 1)
	w.must(ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: []string{reads[0].ID}}, As: reads[0].Row, Verdict: "ok", Finding: "good", Usage: usage, Who: reads[0].Row}))
	bad := h.snap().Readers.Of("s1-2")
	require.Len(t, bad, 1)
	w.must(ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: []string{bad[0].ID}}, As: bad[0].Row, Verdict: "broken", Finding: "bad:1", Usage: usage, Who: bad[0].Row}))
	ret := h.snap().Readers.Of("s1-3")
	require.NotEmpty(t, ret)
	w.must(ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: []string{ret[0].ID}}, As: ret[0].Row, Return: true, Reason: "no verdict", Usage: usage, Who: ret[0].Row}))

	// and the member may not read a tier's array: the step that once did is refused
	assert.ErrorContains(t, bench.HGet(ctx, config.TierKey("flash"), "routes").Err(), "NOPERM")
}

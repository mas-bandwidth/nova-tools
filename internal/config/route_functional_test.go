//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The route kind against the package's throwaway Postgres
// (pg_functional_test.go) and a throwaway Redis (redis_functional_test.go):
// the row round-trips exactly, the rules hold in the store and in the
// schema, and apply leaves the view the deal reads.

func newRoute(t *testing.T, name string, raw map[string]string) Row {
	t.Helper()
	k, _ := Lookup(KindRoute)
	row, err := k.NewRow(name, raw)
	require.NoError(t, err)
	return row
}

func TestPostgresRouteRoundTripsAndHoldsItsRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := migrated(t)
	direct := newRoute(t, "pro-direct", map[string]string{"tier": "pro", "provider": "deepseek", "model": "deepseek-v4", "tokens": "400000", "deadline": "1800"})
	slashed := newRoute(t, "pro-openrouter", map[string]string{"tier": "pro", "provider": "openrouter", "model": "x-ai/grok-4", "deadline": "1800", "enabled": "false"})
	for _, row := range []Row{direct, slashed} {
		_, err := st.Insert(ctx, KindRoute, row, "t")
		require.NoError(t, err)
	}
	for _, want := range []Row{direct, slashed} {
		got, found, err := st.Get(ctx, KindRoute, want.Name)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, want.Fields, got.Fields, "%s reads back exactly", want.Name)
	}

	// A set the kind's Check refuses writes nothing; one it accepts is one
	// history row.
	_, _, err := st.Update(ctx, KindRoute, "pro-direct", map[string]string{"deadline": "0"}, "t")
	require.ErrorIs(t, err, ErrInvalid)
	_, id, err := st.Update(ctx, KindRoute, "pro-direct", map[string]string{"tokens": "0"}, "t")
	require.NoError(t, err)
	hist, err := st.History(ctx, KindRoute, "pro-direct")
	require.NoError(t, err)
	require.Len(t, hist, 2, "an add and a set; the refused set left none")
	assert.Equal(t, id, hist[1].ID)

	// The schema is a second wall behind the descriptor.
	walls := []struct {
		name string
		sql  string
	}{
		{name: "frontier", sql: `INSERT INTO config.routes (name, tier, provider, model, deadline) VALUES ('w1', 'frontier', 'p', 'm', 60)`},
		{name: "a slashed provider", sql: `INSERT INTO config.routes (name, tier, provider, model, deadline) VALUES ('w2', 'pro', 'a/b', 'm', 60)`},
		{name: "a model with a blank", sql: `INSERT INTO config.routes (name, tier, provider, model, deadline) VALUES ('w3', 'pro', 'p', 'a b', 60)`},
		{name: "deadline 0", sql: `INSERT INTO config.routes (name, tier, provider, model, deadline) VALUES ('w4', 'pro', 'p', 'm', 0)`},
	}
	for _, w := range walls {
		_, err := st.db.ExecContext(ctx, w.sql)
		assert.Error(t, err, w.name)
	}
}

func TestApplyWritesTheRouteViewTheDealReads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := migrated(t)
	for _, row := range []Row{
		newRoute(t, "pro-a", map[string]string{"tier": "pro", "provider": "openrouter", "model": "x-ai/grok-4", "tokens": "300000", "deadline": "1800"}),
		newRoute(t, "flash-b", map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "600", "tokens": "3"}),
	} {
		_, err := st.Insert(ctx, KindRoute, row, "t")
		require.NoError(t, err)
	}
	res := applyKinds(t, st, ap, "t")
	assert.Equal(t, 2, res[KindRoute].Add)

	assert.ElementsMatch(t, []string{"pro-a", "flash-b"}, c.SMembers(ctx, RoutesKey).Val())
	got := c.HGetAll(ctx, RouteKey("pro-a")).Val()
	want := map[string]string{
		"name": "pro-a", "tier": "pro", "provider": "openrouter", "model": "x-ai/grok-4",
		"tokens": "300000", "deadline": "1800", "enabled": "true",
	}
	for f, v := range want {
		assert.Equal(t, v, got[f], "route:pro-a %s", f)
	}
	assert.NotEmpty(t, got["at"])
	assert.Equal(t, len(want)+2, len(got), "the fields, rev and at, and no log: %v", got)
	assert.Equal(t, c.HGet(ctx, DeclKey, "rev:route").Val(), got["rev"])

	// A field changed by hand is put back; a removed row's keys go, with a
	// receipt.
	c.HSet(ctx, RouteKey("flash-b"), "tokens", "9")
	_, err := st.Delete(ctx, KindRoute, "pro-a", "t")
	require.NoError(t, err)
	logBefore := c.XLen(ctx, CapLogKey).Val()
	var ops []string
	_, err = Apply(ctx, st, ap, KindRoute, "t", false, func(op Op) { ops = append(ops, OpLine("APPLY", KindRoute, op)) })
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=route name=flash-b changed=tokens", "APPLY REMOVE kind=route name=pro-a"}, ops)
	assert.Equal(t, "3", c.HGet(ctx, RouteKey("flash-b"), "tokens").Val())
	assert.Zero(t, c.Exists(ctx, RouteKey("pro-a")).Val())
	assert.False(t, c.SIsMember(ctx, RoutesKey, "pro-a").Val())
	assert.Equal(t, logBefore+1, c.XLen(ctx, CapLogKey).Val(), "the remove leaves one config-remove receipt")
	views, applied, err := ap.Read(ctx, KindRoute)
	require.NoError(t, err)
	rev, _ := st.Rev(ctx, KindRoute)
	assert.Equal(t, rev, applied, "revision parity after apply")
	rows, _ := st.List(ctx, KindRoute)
	for _, row := range rows {
		assert.Equal(t, View(row.Fields), views[row.Name], "the view of %s is its row", row.Name)
	}
}

// The tier kind on Postgres and Redis: migrate makes the flash and pro rows, a set
// writes the ordered array (a name repeated kept), a route that is no row, disabled
// or of another tier is refused, the schema refuses any other tier, and apply writes
// tier:<name> beside the routes, which the deal reads.
func TestTheTierArrayRoundTripsAndApplyWritesIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := migrated(t)
	tiers, err := st.List(ctx, KindTier)
	require.NoError(t, err)
	require.Len(t, tiers, 2, "migrate makes flash and pro")
	for _, row := range []Row{
		newRoute(t, "flash-a", map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "600"}),
		newRoute(t, "flash-off", map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "600", "enabled": "false"}),
		newRoute(t, "pro-a", map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "600"}),
	} {
		_, err := st.Insert(ctx, KindRoute, row, "t")
		require.NoError(t, err)
	}
	for routes, refusal := range map[string]string{"nope": "names no route row", "flash-off": "names a disabled route", "pro-a": "names a route of tier pro"} {
		_, _, err := st.Update(ctx, KindTier, "flash", map[string]string{"routes": routes}, "t")
		require.True(t, Refused(err), "%s: %v", routes, err)
		assert.Contains(t, err.Error(), refusal)
	}
	_, _, err = st.Update(ctx, KindTier, "flash", map[string]string{"routes": "flash-a,flash-a"}, "t")
	require.NoError(t, err)
	got, _, err := st.Get(ctx, KindTier, "flash")
	require.NoError(t, err)
	assert.Equal(t, "flash-a,flash-a", got.Fields["routes"], "the order and the repeat are kept")
	_, err = st.db.ExecContext(ctx, `INSERT INTO config.tiers (name) VALUES ('frontier')`)
	assert.Error(t, err, "the schema holds no other tier")

	applyKinds(t, st, ap, "t")
	assert.Equal(t, "flash-a,flash-a", c.HGet(ctx, TierKey("flash"), "routes").Val())
	assert.ElementsMatch(t, []string{"flash", "pro"}, c.SMembers(ctx, TiersKey).Val())
}

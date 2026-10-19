//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderMigrationSeedsRoutesAndEnforcesTheReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	migrations, err := Migrations()
	require.NoError(t, err)
	for _, m := range migrations {
		if m.Version >= 41 {
			break
		}
		require.NoError(t, st.applyOne(ctx, m), m.Name)
	}
	_, err = st.db.ExecContext(ctx, `INSERT INTO config.routes(name,tier,provider,model,deadline) VALUES ('pro-a','pro','opencode','m',60),('pro-b','pro','opencode','m',60),('heavy-a','heavy','subscription-claude','m',60)`)
	require.NoError(t, err)
	from, to, applied, err := st.Migrate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 40, from)
	assert.Equal(t, newestVersion(t, migrations), to)
	assert.Equal(t, versionsFrom(migrations, 41), applied)
	rows, err := st.List(ctx, KindProvider)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, "false", row.Fields["funded"])
		assert.Equal(t, "metered", row.Fields["billing"])
		assert.Empty(t, row.Fields["balance_usd"])
		assert.Equal(t, "made by migrate from the routes: set --funded and --billing", row.Fields["note"])
	}
	_, err = st.db.ExecContext(ctx, `INSERT INTO config.routes(name,tier,provider,model,deadline) VALUES ('bad','pro','absent','m',60)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "routes_provider_fkey")
	_, err = st.db.ExecContext(ctx, `DELETE FROM config.providers WHERE name='opencode'`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "routes_provider_fkey")
	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"owner": "operator"}, "operator")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindProvider, "opencode", map[string]string{"billing": "both", "funded": "true"}, "operator")
	require.NoError(t, err)
	// The SQL is idempotent too: re-running it does not overwrite declared facts.
	_, err = st.db.ExecContext(ctx, migrations[40].SQL)
	require.NoError(t, err)
	declared, found, err := st.Get(ctx, KindProvider, "opencode")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "true", declared.Fields["funded"])
	assert.Equal(t, "both", declared.Fields["billing"])
	from, to, applied, err = st.Migrate(ctx)
	require.NoError(t, err)
	assert.Equal(t, newestVersion(t, migrations), from)
	assert.Equal(t, from, to)
	assert.Empty(t, applied)
}

func TestProviderApplyWritesAndRemovesTheFundingView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := migrated(t)
	addProvider(t, st, "opencode")
	_, _, err := st.Update(ctx, KindProvider, "opencode", map[string]string{"funded": "true", "billing": "both", "balance_usd": "238", "balance_as_of": "2026-10-13"}, "operator")
	require.NoError(t, err)
	ap, c := redisApplier(t)
	_, err = Apply(ctx, st, ap, KindProvider, "operator", false, func(Op) {})
	require.NoError(t, err)
	assert.Equal(t, []string{"opencode"}, c.SMembers(ctx, ProvidersKey).Val())
	hash := c.HGetAll(ctx, ProviderKey("opencode")).Val()
	assert.Equal(t, "true", hash["funded"])
	assert.Equal(t, "both", hash["billing"])
	assert.Equal(t, "238", hash["balance_usd"])
	assert.Equal(t, "2026-10-13", hash["balance_as_of"])
	views, rev, err := ap.Read(ctx, KindProvider)
	require.NoError(t, err)
	assert.Equal(t, int64(3), rev)
	assert.Equal(t, "true", views["opencode"]["funded"])
	_, err = st.Delete(ctx, KindProvider, "opencode", "operator")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindProvider, "operator", false, func(Op) {})
	require.NoError(t, err)
	assert.Zero(t, c.Exists(ctx, ProviderKey("opencode")).Val())
	assert.Empty(t, c.SMembers(ctx, ProvidersKey).Val())
}

func TestProviderUpgradeRefusesEveryLegacyWordBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	all, err := Migrations()
	require.NoError(t, err)
	for _, m := range all {
		if m.Version >= 40 {
			break
		}
		require.NoError(t, st.applyOne(ctx, m))
	}
	_, err = st.db.ExecContext(ctx, `INSERT INTO config.routes(name,tier,provider,model,deadline) VALUES ('a','pro','OpenRouter','m',60),('b','pro','my_provider','m',60),('c','pro','vendor.ai','m',60)`)
	require.NoError(t, err)
	ledger, err := st.Applied(ctx)
	require.NoError(t, err)
	from, to, applied, err := st.Migrate(ctx)
	var legacy *ProviderNamesError
	require.ErrorAs(t, err, &legacy)
	assert.Len(t, legacy.Routes, 3)
	assert.Equal(t, 39, from)
	assert.Equal(t, from, to)
	assert.Empty(t, applied, "even migration40 must not write first")
	for _, want := range []string{"OpenRouter", "my_provider", "vendor.ai", "route set a --provider <new>", "route set b --provider <new>", "route set c --provider <new>", "previous/schema40"} {
		assert.Contains(t, err.Error(), want)
	}
	after, err := st.Applied(ctx)
	require.NoError(t, err)
	assert.Equal(t, ledger, after)
	var absent bool
	require.NoError(t, st.db.QueryRowContext(ctx, `SELECT to_regclass('config.providers') IS NULL`).Scan(&absent))
	assert.True(t, absent)
	var words []string
	rows, err := st.db.QueryContext(ctx, `SELECT provider FROM config.routes ORDER BY name`)
	require.NoError(t, err)
	for rows.Next() {
		var word string
		require.NoError(t, rows.Scan(&word))
		words = append(words, word)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Equal(t, []string{"OpenRouter", "my_provider", "vendor.ai"}, words, "no silent rename")
	// Fixture-only correction in the old schema. Production remediation names
	// the matching previous binary; a separate CLI receipt verifies that path.
	_, err = st.db.ExecContext(ctx, `UPDATE config.routes SET provider=CASE name WHEN 'a' THEN 'openrouter' WHEN 'b' THEN 'my-provider' ELSE 'vendor-ai' END`)
	require.NoError(t, err)
	from, to, applied, err = st.Migrate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 39, from)
	assert.Equal(t, newestVersion(t, all), to)
	assert.Equal(t, versionsFrom(all, 40), applied)
	providers, err := st.List(ctx, KindProvider)
	require.NoError(t, err)
	require.Len(t, providers, 3)
	for _, p := range providers {
		assert.Equal(t, "false", p.Fields["funded"])
	}
}

// TestFundingRefusesInThePGStore is the refuser in Postgres, the tier the
// production store runs: a route may not enable on an unfunded provider and a
// non-owner may not fund it (docs/SPEC-CONFIG.md, "Funding is the owner's").
func TestFundingRefusesInThePGStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := migrated(t)

	provider, _ := Lookup(KindProvider)
	p, err := provider.NewRow("p1", map[string]string{"billing": "metered", "funded": "false"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindProvider, p, "machine")
	require.NoError(t, err)

	route, _ := Lookup(KindRoute)
	r, err := route.NewRow("r1", map[string]string{"tier": "pro", "provider": "p1", "model": "m", "deadline": "60", "enabled": "true"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindRoute, r, "machine")
	require.ErrorIs(t, err, ErrOwnerOnly)

	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"owner": "glenn"}, "glenn")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "machine")
	require.ErrorIs(t, err, ErrOwnerOnly)
	_, _, err = st.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "glenn")
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindRoute, r, "machine")
	require.NoError(t, err)
}

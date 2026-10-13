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
	assert.Equal(t, 41, to)
	assert.Equal(t, []int{41}, applied)
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
	assert.Equal(t, 41, from)
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
	assert.Equal(t, int64(2), rev)
	assert.Equal(t, "true", views["opencode"]["funded"])
	_, err = st.Delete(ctx, KindProvider, "opencode", "operator")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindProvider, "operator", false, func(Op) {})
	require.NoError(t, err)
	assert.Zero(t, c.Exists(ctx, ProviderKey("opencode")).Val())
	assert.Empty(t, c.SMembers(ctx, ProvidersKey).Val())
}

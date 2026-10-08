//go:build functional

package config

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationKeepsTheTiersFallback: migration 37 on a store whose friend rows carry
// only tiers adds the model table and the models column and seeds no row: every friend
// keeps her tiers, and her class is the highest of them. The migration run again changes
// nothing, and a friend given models afterwards takes her class from the first.
func TestMigrationKeepsTheTiersFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	all, err := Migrations()
	require.NoError(t, err)
	var m37 Migration
	for _, m := range all {
		if m.Version < 37 {
			require.NoError(t, st.applyOne(ctx, m), "migration %d", m.Version)
		}
		if m.Version == 37 {
			m37 = m
		}
	}
	require.Equal(t, "0037_friend_models.sql", m37.Name)
	for _, r := range [][2]string{{"f-heavy", "heavy,pro"}, {"f-pro", "flash,pro"}, {"f-flash", "flash"}, {"f-all", "flash,frontier,heavy,pro"}} {
		_, err := st.db.ExecContext(ctx, `INSERT INTO config.friends (name, slots, tiers) VALUES ($1, 1, $2)`, r[0], r[1])
		require.NoError(t, err, "friend %s", r[0])
	}
	_, _, applied, err := st.Migrate(ctx)
	require.NoError(t, err)
	require.Equal(t, 37, applied[0], "migration 37 applied")
	_, err = st.db.ExecContext(ctx, m37.SQL)
	require.NoError(t, err, "the migration run again")

	models, err := st.List(ctx, KindModel)
	require.NoError(t, err)
	assert.Empty(t, models, "no model row is seeded")
	class := func() map[string]string {
		rows, err := st.List(ctx, KindFriend)
		require.NoError(t, err)
		tiers, err := ModelTiers(ctx, st)
		require.NoError(t, err)
		got := map[string]string{}
		for _, r := range rows {
			c, dealt, err := FriendClass(r, tiers)
			require.NoError(t, err, "friend %s", r.Name)
			got[r.Name] = fmt.Sprintf("models=%s class=%s tiers=%s", r.Fields["models"], c, strings.Join(dealt, ","))
		}
		return got
	}
	assert.Equal(t, map[string]string{
		"f-heavy": "models= class=heavy tiers=heavy,pro",
		"f-pro":   "models= class=pro tiers=flash,pro",
		"f-flash": "models= class=flash tiers=flash",
		"f-all":   "models= class=frontier tiers=flash,frontier,heavy,pro",
	}, class(), "every row keeps its tiers fallback, the highest its class")

	addModels(t, st, "m-heavy", "heavy", "m-pro", "pro")
	_, _, err = st.Update(ctx, KindFriend, "f-heavy", map[string]string{"models": "m-heavy,m-pro", "tiers": ""}, "t")
	require.NoError(t, err)
	assert.Equal(t, "models=m-heavy,m-pro class=heavy tiers=heavy,pro", class()["f-heavy"], "her class from her first model")
}

//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationThirtyThreeSeedsTheAppliesMask is the route applies mask's seed
// (internal/config/migrations/0033_route_applies.sql; docs/SPEC-CONFIG.md,
// route): a route named pro-* gets applies=friends, flash-* gets applies=fleet,
// every other route keeps all; a pro route disabled to keep pro work off the
// fleet (the owner, 2026-10-04, 5:38 PM) is enabled again with the seed's note,
// and a pro route disabled for another measured reason keeps `enabled=false`
// and its note. The file run again changes nothing.
func TestMigrationThirtyThreeSeedsTheAppliesMask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	all, err := Migrations()
	require.NoError(t, err)
	var m Migration
	for _, x := range all {
		if x.Version == 33 {
			m = x
			break
		}
		require.NoError(t, st.applyOne(ctx, x), "migration %s", x.Name)
	}
	require.Equal(t, "0033_route_applies.sql", m.Name, "0033 is the applies migration")
	insert := `INSERT INTO config.routes (name, tier, provider, model, deadline, enabled, note) VALUES `
	_, err = st.db.ExecContext(ctx, insert+
		"('pro-plain', 'pro', 'p', 'm', 60, true, ''),"+
		"('pro-kept-off', 'pro', 'p', 'm', 60, false, 'flash only to the fleet'),"+
		"('pro-provider-fail', 'pro', 'p', 'm', 60, false, 'ok 0/5, the provider failed'),"+
		"('flash-a', 'flash', 'p', 'm', 60, true, ''),"+
		"('heavy-a', 'heavy', 'p', 'm', 60, true, '')")
	require.NoError(t, err)
	require.NoError(t, st.applyOne(ctx, m))
	type row struct {
		applies, note string
		enabled       bool
	}
	read := func() map[string]row {
		t.Helper()
		rows, err := st.db.QueryContext(ctx, `SELECT name, applies, enabled, note FROM config.routes`)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]row{}
		for rows.Next() {
			var name string
			var r row
			require.NoError(t, rows.Scan(&name, &r.applies, &r.enabled, &r.note))
			out[name] = r
		}
		require.NoError(t, rows.Err())
		return out
	}
	want := map[string]row{
		"pro-plain":         {applies: "friends", enabled: true},
		"pro-kept-off":      {applies: "friends", enabled: true, note: "applies friends: pro routes run on friends only (the owner, 2026-10-04)"},
		"pro-provider-fail": {applies: "friends", enabled: false, note: "ok 0/5, the provider failed"},
		"flash-a":           {applies: "fleet", enabled: true},
		"heavy-a":           {applies: "all", enabled: true},
	}
	got := read()
	assert.Equal(t, want, got, "the seed")
	// the file run again changes nothing: the seed is idempotent
	_, err = st.db.ExecContext(ctx, m.SQL)
	require.NoError(t, err, "the file run again")
	assert.Equal(t, got, read(), "a second run changes nothing")
}

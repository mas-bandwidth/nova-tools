//go:build functional

package main

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalOldStore (short: macOS caps the socket path, which holds the test name, at 103 bytes): a store made
// before the rented table (testdata/before-rented.sql, the schema.sql of the release before it) takes schema.sql,
// which leaves every table that release reads exactly as it was; a rented aws box lands in no row such a release
// reads, so its inventory cannot hold it; an older tool's writes still go through.
func TestFunctionalOldStore(t *testing.T) {
	t.Parallel()
	dsn := startPG(t, "testdata/before-rented.sql")
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	columns := func() []string {
		rows, err := c.Query(ctx, `SELECT table_name || '.' || column_name || ' ' || data_type || ' ' || coalesce(column_default, '') FROM information_schema.columns WHERE table_schema = 'config' AND table_name <> 'rented' ORDER BY table_name, ordinal_position`)
		require.NoError(t, err)
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		return out
	}
	before := columns()
	env := map[string]string{"NOVA_PG_DSN": dsn}
	code, _, errs := realRun(t, env, "rented", "list")
	assert.Equal(t, 2, code, "the new tool on the old store says how to make it current")
	assert.Contains(t, errs, "nova-config schema")

	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	_, err = c.Exec(ctx, string(schema))
	require.NoError(t, err)
	assert.Equal(t, before, columns(), "the tables an older release reads keep exactly their columns")

	code, _, errs = realRun(t, env, rentArgs("spill-1")...)
	require.Equal(t, 0, code, errs)
	var machines int
	require.NoError(t, c.QueryRow(ctx, `SELECT count(*) FROM config.machines`).Scan(&machines))
	assert.Equal(t, 0, machines, "an older release reads machines for its inventory: the aws box is not there")
	code, out, errs := realRun(t, env, "inventory", "--list")
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, out, "spill-1")

	_, err = c.Exec(ctx, `INSERT INTO config.machines (name, "user", roles, note) VALUES ('old-1', 'nova', 'bench', 'written as an older tool writes')`)
	require.NoError(t, err, "an older tool's add names only its own columns")
	_, err = c.Exec(ctx, `UPDATE config.machines SET note = 'set by an older tool' WHERE name = 'old-1'`)
	require.NoError(t, err)
	_, err = c.Exec(ctx, `UPDATE config.rented SET price = '4,06' WHERE name = 'spill-1'`)
	assert.Error(t, err, "the store refuses a price that is not a decimal, whoever writes it")
	_, err = c.Exec(ctx, `UPDATE config.rented SET rented_at = 'yesterday' WHERE name = 'spill-1'`)
	assert.Error(t, err, "and a rented_at that is not an RFC 3339 UTC time")
	_, err = c.Exec(ctx, `INSERT INTO config.rented (name, provider, purpose, rented_by, rented_at) VALUES ('r-2', 'aws', '', 'a', '2026-11-13T00:00:00Z')`)
	assert.Error(t, err, "and a rented box with no purpose")
}

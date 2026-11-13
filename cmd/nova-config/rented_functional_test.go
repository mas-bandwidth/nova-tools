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

// TestFunctionalOldStore (short: macOS caps the socket path, which holds the test name, at 103 bytes): a store made before the rented columns (testdata/before-rented.sql, the
// schema.sql of the release before them) takes schema.sql; its rows read as owned with the rest empty; a write naming
// only the old columns, as an older nova-config makes, still goes through and leaves the new ones at their defaults.
func TestFunctionalOldStore(t *testing.T) {
	t.Parallel()
	dsn := startPG(t, "testdata/before-rented.sql")
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	_, err = c.Exec(ctx, `INSERT INTO config.machines (name, "user", roles) VALUES ('old-1', 'nova', 'bench')`)
	require.NoError(t, err)
	env := map[string]string{"NOVA_PG_DSN": dsn}
	code, _, errs := realRun(t, env, "machine", "list")
	assert.Equal(t, 2, code, "the new tool on the old store says how to make it current")
	assert.Contains(t, errs, "nova-config schema")

	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	_, err = c.Exec(ctx, string(schema))
	require.NoError(t, err)
	code, out, errs := realRun(t, env, "machine", "show", "old-1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, rentedNone)

	_, err = c.Exec(ctx, `INSERT INTO config.machines (name, "user", roles, note) VALUES ('old-2', 'nova', 'bench', 'written as an older tool writes')`)
	require.NoError(t, err, "an older tool's add names only its own columns")
	_, err = c.Exec(ctx, `UPDATE config.machines SET note = 'set by an older tool' WHERE name = 'old-1'`)
	require.NoError(t, err)
	code, out, errs = realRun(t, env, "machine", "show", "old-2")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, rentedNone)

	args := rentArgs("spill-1")
	code, _, errs = realRun(t, env, args...)
	require.Equal(t, 0, code, errs)
	code, out, errs = realRun(t, env, "machine", "show", "spill-1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, " provider=aws provider_id=i-0abc market=spot spot_request=sir-1 price=0.59 price_from=provider ")
	_, err = c.Exec(ctx, `UPDATE config.machines SET price = '4,06' WHERE name = 'spill-1'`)
	assert.Error(t, err, "the store refuses a price that is not a decimal, whoever writes it")
	_, err = c.Exec(ctx, `UPDATE config.machines SET rented_at = 'yesterday' WHERE name = 'spill-1'`)
	assert.Error(t, err, "and a rented_at that is not an RFC 3339 UTC time")
}

//go:build functional

package main

import (
	"context"
	"fmt"
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
	_, err = c.Exec(ctx, `INSERT INTO config.rented (name, provider, purpose, rented_by, rented_at) VALUES ('r-3', 'aws', 'p', 'a', '2026-11-13T00:00:00Z')`)
	assert.Error(t, err, "and an aws box with no market")

	_, err = c.Exec(ctx, `INSERT INTO config.machines (name, "user") VALUES ('spill-1', 'nova')`)
	assert.ErrorContains(t, err, "is a rented aws box, which no play may reach", "an older tool's machine add cannot put an aws box in reach of a play")
	_, err = c.Exec(ctx, `UPDATE config.machines SET name = 'spill-1' WHERE name = 'old-1'`)
	assert.ErrorContains(t, err, "is a rented aws box", "nor a rename onto one")
	code, _, errs = realRun(t, env, rentArgs("old-1")...)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "cannot share its name with machine old-1")

	_, err = c.Exec(ctx, `DELETE FROM config.rented WHERE name = 'spill-1'`)
	assert.ErrorContains(t, err, "still has a box or spot request", "the store keeps a record that holds a handle, whoever deletes it")
	_, err = c.Exec(ctx, `UPDATE config.rented SET provider_id = '', spot_request = '' WHERE name = 'spill-1'`)
	require.NoError(t, err)
	_, err = c.Exec(ctx, `DELETE FROM config.rented WHERE name = 'spill-1'`)
	assert.NoError(t, err, "with both cleared it goes")
}

// TestFunctionalRentedRace: a machine row and an aws rented row of the same new name, written at the same moment by
// two transactions (as two tools, of any release, would), never both commit: the triggers serialize on the name.
// Without the lock each reads the other as absent; many rounds make that show.
func TestFunctionalRentedRace(t *testing.T) {
	t.Parallel()
	dsn := startPG(t)
	ctx := context.Background()
	conn := func() *pgx.Conn {
		c, err := pgx.Connect(ctx, dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close(ctx) }) // ignored: closing a test connection
		return c
	}
	a, b, check := conn(), conn(), conn()
	for i := range 200 {
		name := fmt.Sprintf("race-%d", i)
		ta, err := a.Begin(ctx)
		require.NoError(t, err)
		tb, err := b.Begin(ctx)
		require.NoError(t, err)
		// Both inserts run before either commits: the second either waits on the first's name lock or, with no
		// lock, reads the other table as empty.
		done := make(chan error, 1)
		_, errA := ta.Exec(ctx, `INSERT INTO config.machines (name, "user") VALUES ($1, 'nova')`, name)
		go func() {
			_, err := tb.Exec(ctx, `INSERT INTO config.rented (name, provider, market, purpose, rented_by, rented_at) VALUES ($1, 'aws', 'on-demand', 'p', 'a', '2026-11-13T00:00:00Z')`, name)
			if err == nil {
				err = tb.Commit(ctx)
			}
			done <- err
		}()
		if errA == nil {
			errA = ta.Commit(ctx)
		}
		errB := <-done
		_ = ta.Rollback(ctx) // ignored: a no-op after a commit
		_ = tb.Rollback(ctx) // ignored: a no-op after a commit
		var both int
		require.NoError(t, check.QueryRow(ctx, `SELECT count(*) FROM config.machines m JOIN config.rented r USING (name) WHERE r.provider = 'aws' AND name = $1`, name).Scan(&both))
		require.Equal(t, 0, both, "round %d: a machine row and an aws box of one name both committed (machine: %v, rented: %v)", i, errA, errB)
		require.False(t, errA == nil && errB == nil, "round %d: both writes went through", i)
	}
}

// TestFunctionalRentedFixed: the store holds a held handle's identity for every writer: provider, market and name stay,
// and a handle is given or cleared, never replaced.
func TestFunctionalRentedFixed(t *testing.T) {
	t.Parallel()
	dsn := startPG(t)
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	_, err = c.Exec(ctx, `INSERT INTO config.rented (name, provider, provider_id, purpose, rented_by, rented_at) VALUES ('lat-1', 'latitude', 'sv_1', 'p', 'a', '2026-11-13T00:00:00Z')`)
	require.NoError(t, err)
	for _, set := range []string{`provider = 'aws', market = 'on-demand'`, `provider_id = 'sv_2'`, `name = 'lat-2'`} {
		_, err = c.Exec(ctx, `UPDATE config.rented SET `+set+` WHERE name = 'lat-1'`)
		assert.ErrorContains(t, err, "stay as they are until each handle is cleared", set)
	}
	_, err = c.Exec(ctx, `UPDATE config.rented SET provider_id = '' WHERE name = 'lat-1'`)
	require.NoError(t, err)
	_, err = c.Exec(ctx, `UPDATE config.rented SET provider = 'aws', market = 'on-demand' WHERE name = 'lat-1'`)
	assert.NoError(t, err, "with nothing held it is free")

	_, err = c.Exec(ctx, `INSERT INTO config.rented (name, provider, provider_id, purpose, rented_by, rented_at) VALUES ('lat-3', 'latitude', 'sv_3', 'p', 'a', '2026-11-13T00:00:00Z')`)
	require.NoError(t, err)
	_, err = c.Exec(ctx, `INSERT INTO config.rented (name, provider, provider_id, purpose, rented_by, rented_at) VALUES ('lat-4', 'latitude', 'sv_3', 'p', 'a', '2026-11-13T00:00:00Z')`)
	assert.ErrorContains(t, err, "rented_box", "one record per box")
	_, err = c.Exec(ctx, `TRUNCATE config.rented`)
	assert.ErrorContains(t, err, "never emptied at once", "a truncate, which fires no row trigger, is refused")
	_, err = c.Exec(ctx, `UPDATE config.rented SET provider_id = ''`)
	require.NoError(t, err)
	_, err = c.Exec(ctx, `DELETE FROM config.rented`)
	require.NoError(t, err)
	_, err = c.Exec(ctx, `TRUNCATE config.rented`)
	assert.ErrorContains(t, err, "never emptied at once", "and refused when empty too")

	// The ordered case: A, serializable, takes its snapshot; B, read committed, adds machine m-ser and commits; A
	// then adds an aws box m-ser. A's snapshot cannot see B's row, so A is refused for its isolation.
	b, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = b.Close(ctx) }() // ignored: closing the second connection
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	require.NoError(t, err)
	var n int
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM config.machines`).Scan(&n)) // takes the snapshot
	_, err = b.Exec(ctx, `INSERT INTO config.machines (name, "user") VALUES ('m-ser', 'nova')`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO config.rented (name, provider, market, purpose, rented_by, rented_at) VALUES ('m-ser', 'aws', 'on-demand', 'p', 'a', '2026-11-13T00:00:00Z')`)
	assert.ErrorContains(t, err, "runs at read committed", "a write whose snapshot predates the lock is refused")
	_ = tx.Rollback(ctx) // ignored: the transaction failed
}

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

// A store whose machines table has no runner_env column refuses with the schema line, and `nova-config schema` applied
// to it again adds the column with its default: a store is made current by the same SQL that made it.
func TestFunctionalRunnerEnv(t *testing.T) {
	t.Parallel()
	dsn := startPG(t)
	env := map[string]string{"NOVA_PG_DSN": dsn}
	code, _, errs := realRun(t, env, "machine", "add", "m1", "--user", "nova", "--actor", "a1")
	require.Equal(t, 0, code, errs)
	c, err := pgx.Connect(context.Background(), dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }() // ignored: closing the test's connection
	_, err = c.Exec(context.Background(), "ALTER TABLE config.machines DROP COLUMN runner_env")
	require.NoError(t, err)

	code, _, errs = realRun(t, env, "machine", "list")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, `store has no current schema; run: nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"`)

	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	_, err = c.Exec(context.Background(), string(schema))
	require.NoError(t, err)
	code, out, errs := realRun(t, env, "machine", "show", "m1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, " runner_env={} ")
	code, _, errs = realRun(t, env, "machine", "set", "m1", "--runner_env", `{"TMPDIR":"/home/nova/.cache/runner-tmp"}`, "--actor", "a1")
	require.Equal(t, 0, code, errs)
	_, err = c.Exec(context.Background(), "UPDATE config.machines SET runner_env = 'nope' WHERE name = 'm1'")
	assert.Error(t, err, "the column keeps its check")
}

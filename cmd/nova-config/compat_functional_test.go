//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oldReads are the columns an older release's library reads from each table, in order: the json tags of its row
// types (git show v2.0.3:config/lib/rows.go; git show v2.0.75:modules/config/rows.go). A nova-sprint built on one
// runs exactly these queries, through oldListSQL.
var oldReads = map[string]map[string][]string{
	"v2.0.3": {
		"machines": {"address", "public", "user", "os", "arch", "cores", "roles", "runners", "note"},
		"friends":  {"harness", "model", "width", "tiers", "pronouns", "note", "enabled"},
		"services": {"machine", "argv", "seat", "keys", "every", "keepalive", "enabled", "env", "log"},
	},
	"v2.0.75": {
		"machines": {"address", "public", "user", "os", "arch", "cores", "roles", "runners", "runner_scopes", "windows_runners", "windows_admin_runners", "runner_env", "note"},
		"friends":  {"harness", "model", "width", "tiers", "pronouns", "note", "enabled"},
		"services": {"machine", "argv", "seat", "keys", "every", "keepalive", "enabled", "env", "log"},
	},
}

// oldListSQL is the query listSQL builds in v2.0.3 (config/lib/store.go) and v2.0.75 (modules/config/store.go), the
// same text in both, so it does not change when this release's listSQL does.
func oldListSQL(table string, columns []string) string {
	cols := []string{"name"}
	for _, c := range columns {
		cols = append(cols, `coalesce("`+c+`"::text, '')`)
	}
	stamp := `to_char(%s AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	cols = append(cols, fmt.Sprintf(stamp, "created_at"), fmt.Sprintf(stamp, "updated_at"))
	return "SELECT " + strings.Join(cols, ", ") + " FROM config." + table + " ORDER BY name"
}

// TestFunctionalV1Aside: a store still holding the v1 tools' config.providers, and a v1 table whose
// foreign key names it, takes schema.sql: the old table moves to config.providers_v1 with its rows, and providers
// and models work.
func TestFunctionalV1Aside(t *testing.T) {
	t.Parallel()
	dsn := startPG(t, "testdata/before-providers.sql")
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	exec := func(stmt string) {
		t.Helper()
		_, err := c.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	exec(`CREATE TABLE config.providers (name text PRIMARY KEY CONSTRAINT providers_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'), billing text NOT NULL, base_url text NOT NULL DEFAULT '', key_env text NOT NULL DEFAULT '')`)
	exec(`CREATE TABLE config.routes (name text PRIMARY KEY, provider text NOT NULL REFERENCES config.providers (name))`)
	exec(`INSERT INTO config.providers (name, billing, base_url, key_env) VALUES ('minimax', 'metered', 'https://api.minimax.io/anthropic/v1', 'MINIMAX_API_KEY')`)
	exec(`INSERT INTO config.routes VALUES ('r1', 'minimax')`)

	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	exec(string(schema))
	exec(string(schema)) // twice: the move happens once

	var keyEnv string
	require.NoError(t, c.QueryRow(ctx, `SELECT key_env FROM config.providers_v1 WHERE name = 'minimax'`).Scan(&keyEnv))
	assert.Equal(t, "MINIMAX_API_KEY", keyEnv, "the v1 rows are kept")
	env := map[string]string{"NOVA_PG_DSN": dsn}
	code, _, errs := realRun(t, env, "provider", "add", "minimax", "--url", "https://api.minimax.io/anthropic", "--api", "anthropic", "--key", "MINIMAX_API_KEY", "--actor", "a")
	require.Equal(t, 0, code, errs)
	code, _, errs = realRun(t, env, "model", "add", "minimax/m-1", "--harness", "claude", "--actor", "a")
	require.Equal(t, 0, code, errs)
}

// TestFunctionalOldReaders: a store made by v2.0.75 (testdata/before-providers.sql, its schema.sql), holding rows its
// tool wrote, takes schema.sql. Every query an older release's library runs on machines, friends and services then
// reads the same rows with the same values; an older tool's writes, width and tiers included, still go through; and
// this release reads those rows and writes providers and models beside them, leaving width and tiers as they were.
func TestFunctionalOldReaders(t *testing.T) {
	t.Parallel()
	dsn := startPG(t, "testdata/before-providers.sql")
	env := map[string]string{"NOVA_PG_DSN": dsn}
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	exec := func(stmt string) {
		t.Helper()
		_, err := c.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	// rows as v2.0.75's nova-config writes them: every column its kinds name
	exec(`INSERT INTO config.machines (name, "address", "public", "user", "os", "arch", "cores", "roles", "runners", "runner_scopes", "windows_runners", "windows_admin_runners", "runner_env", "note") VALUES ('m1', 'm1.tail', NULL, 'nova', 'linux', 'amd64', 8, 'bench', 0, '', '', '', '{"A":"b"}', 'old')`)
	exec(`INSERT INTO config.services ("name", "machine", "argv", "seat", "keys", "every", "keepalive", "enabled", "env", "log") VALUES ('s1', 'm1', '["/p"]', '', '', 60, false, true, '{}', '')`)
	exec(`INSERT INTO config.friends ("name", "harness", "model", "width", "tiers", "pronouns", "note", "enabled") VALUES ('f1', 'claude', 'opus', 4, 'heavy,pro', 'she/her', '', true)`)
	read := func() map[string][][]any {
		t.Helper()
		out := map[string][][]any{}
		for release, tables := range oldReads {
			for table, cols := range tables {
				rows, err := c.Query(ctx, oldListSQL(table, cols))
				require.NoError(t, err, "%s reading %s", release, table)
				got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([]any, error) { return r.Values() })
				require.NoError(t, err, "%s reading %s", release, table)
				out[release+" "+table] = got
			}
		}
		return out
	}
	before := read()
	assert.Equal(t, []any{"f1", "claude", "opus", "4", "heavy,pro", "she/her", "", "true"}, before["v2.0.75 friends"][0][:8])

	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	exec(string(schema))
	exec(string(schema)) // twice: every statement is idempotent
	assert.Equal(t, before, read(), "every older release's query reads the same rows, with the same values")

	// an older tool's writes name only its own columns, width and tiers among them
	exec(`INSERT INTO config.friends ("name", "harness", "model", "width", "tiers", "pronouns", "note", "enabled") VALUES ('f2', 'codex', '', 2, 'pro', NULL, '', true)`)
	exec(`UPDATE config.friends SET "width" = 3, updated_at = now() WHERE name = 'f2'`)
	exec(`INSERT INTO config.machines (name, "address", "public", "user", "os", "arch", "cores", "roles", "runners", "runner_scopes", "windows_runners", "windows_admin_runners", "runner_env", "note") VALUES ('m2', '', NULL, 'nova', '', '', NULL, 'bench', 0, '', '', '', '{}', '')`)

	ok := func(args ...string) string {
		t.Helper()
		code, out, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
		return out
	}
	assert.Contains(t, ok("machine", "show", "m1"), ` roles=bench routes=- runners=0 `, "an old row has no routes")
	assert.Equal(t, `FRIEND name=f1 harness=claude model=opus pronouns=she/her note=- enabled=true`, strings.SplitN(ok("friend", "show", "f1"), " created=", 2)[0])
	ok("friend", "set", "f1", "--note", "kin", "--actor", "a")
	ok("friend", "add", "f3", "--harness", "claude", "--actor", "a")
	ok("friend", "add", "f4", "--harness", "codex", "--width", "6", "--tiers", "pro,flash", "--actor", "a")
	ok("friend", "set", "f2", "--width", "5", "--tiers", "heavy", "--actor", "a")
	assert.NotContains(t, ok("friend", "show", "f4"), "width", "the flags write the columns; nothing shows them")
	code, _, errs := realRun(t, env, "friend", "set", "f2", "--width", "0", "--actor", "a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "friend f2 has --width 0")
	ok("machine", "set", "m1", "--routes", "gpu", "--actor", "a")
	ok("provider", "add", "p1", "--url", "https://api.example.com", "--api", "openai", "--actor", "a")
	ok("model", "add", "p1/m-1", "--machine", "m1", "--actor", "a")
	var widths, tiers string
	require.NoError(t, c.QueryRow(ctx, `SELECT string_agg(width::text, ',' ORDER BY name), string_agg(tiers, ';' ORDER BY name) FROM config.friends`).Scan(&widths, &tiers))
	assert.Equal(t, "4,5,1,6", widths, "a write without --width leaves width as written, one without it on an add takes the column default an older nova-sprint reads, and --width writes it")
	assert.Equal(t, "heavy,pro;heavy;flash,frontier,heavy,pro;flash,pro", tiers)
	after := read()
	assert.Len(t, after["v2.0.75 friends"], 4)
	assert.Len(t, after["v2.0.3 machines"], 2, "an older release still reads every machine")

	st, err := config.Open(ctx, dsn, "")
	require.NoError(t, err)
	defer st.Close()
	snap, err := st.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpu", ""}, []string{snap.Machines[0].Routes, snap.Machines[1].Routes})
	assert.Equal(t, "kin", snap.Friends[0].Note)
	require.Len(t, snap.Models, 1)
	assert.Equal(t, []string{"p1", "p1/m-1", "m1"}, []string{snap.Providers[0].Name, snap.Models[0].Name, snap.Models[0].Machine})
}

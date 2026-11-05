//go:build functional

package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wait = 30 * time.Second

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	require.NoError(t, l.Close())
	return port
}

// pgBin finds initdb: on PATH, else in a Debian postgresql directory.
func pgBin(t *testing.T) string {
	if p, err := exec.LookPath("initdb"); err == nil {
		return filepath.Dir(p)
	}
	if found, _ := filepath.Glob("/usr/lib/postgresql/*/bin/initdb"); len(found) > 0 {
		return filepath.Dir(found[len(found)-1])
	}
	t.Skip("initdb not found")
	return ""
}

// startPG starts a PostgreSQL of this test's own on a free local port, makes
// the tables with schema.sql and returns its password-free DSN.
func startPG(t *testing.T, schemas ...string) string {
	t.Helper()
	bin, dir := pgBin(t), t.TempDir()
	out, err := exec.Command(filepath.Join(bin, "initdb"), "-D", filepath.Join(dir, "data"), "-U", "nova", "--auth=trust", "-E", "UTF8").CombinedOutput()
	require.NoError(t, err, "%s", out)
	port := freePort(t)
	cmd := exec.Command(filepath.Join(bin, "postgres"), "-D", filepath.Join(dir, "data"), "-p", port, "-k", dir, "-c", "listen_addresses=127.0.0.1", "-c", "fsync=off")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: the server is ours and disposable
	dsn := "postgres://nova@127.0.0.1:" + port + "/postgres?sslmode=disable"
	var c *pgx.Conn
	require.Eventually(t, func() bool { c, err = pgx.Connect(context.Background(), dsn); return err == nil }, wait, 50*time.Millisecond)
	defer func() { _ = c.Close(context.Background()) }() // ignored: closing the setup connection
	if len(schemas) == 0 {
		schemas = []string{"schema.sql"}
	}
	for _, name := range schemas {
		schema, err := os.ReadFile(name)
		require.NoError(t, err)
		_, err = c.Exec(context.Background(), string(schema))
		require.NoError(t, err, name)
		_, err = c.Exec(context.Background(), string(schema))
		require.NoError(t, err, name+" runs twice")
	}
	return dsn
}

func realRun(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var out, errs bytes.Buffer
	d := deps{getenv: func(k string) string { return env[k] }, hostname: func() (string, error) { return "m1", nil }, openPG: openPG}
	code := run(ctx, args, d, &out, &errs)
	return code, out.String(), errs.String()
}

func TestFunctionalStore(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t)}
	ok := func(args ...string) string {
		t.Helper()
		code, out, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
		return out
	}
	assert.Equal(t, "CONFIG ADD kind=machine name=m1 rev=1\n", ok("machine", "add", "m1", "--user", "nova", "--actor", "a1"))
	ok("loop", "add", "r-m1", "--machine", "m1", "--argv", `["prog","a b"]`, "--keepalive", "true", "--actor", "a1")
	assert.Equal(t, "CONFIG SET kind=loop name=r-m1 rev=3 changed=enabled\n", ok("loop", "set", "r-m1", "--enabled", "false", "--actor", "a1", "--reason", "why"))
	assert.Equal(t, `LOOP name=r-m1 machine=m1 argv=["prog","a\x20b"] seat=- keys=- every=0 keepalive=true enabled=false`+"\nCONFIG LIST kind=loop rows=1\n", ok("loop", "list"))
	assert.Equal(t, "MACHINE name=m1 user=nova address=- os=- arch=- cores=- runners=0 roles=bench note=-\nCONFIG LIST kind=machine rows=1\n", ok("machine", "list"))
	show := ok("machine", "show", "m1")
	assert.Regexp(t, `created=20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ updated=20\d\d-.*Z loops=r-m1\n$`, show)
	ok("machine", "set", "m1", "--cores", "4", "--note", "a note", "--actor", "a1")
	ok("machine", "set", "m1", "--cores", "default", "--actor", "a1")
	ok("machine", "set", "m1", "--cores", "16", "--os", "linux", "--arch", "amd64", "--roles", "bench,services", "--actor", "a1")
	ok("fleet", "set", "--pg_dsn", "postgres://nova@m1/postgres", "--actor", "a1")

	c, err := pgx.Connect(context.Background(), env["NOVA_PG_DSN"])
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }() // ignored: closing the check connection
	var before, after, actor string
	require.NoError(t, c.QueryRow(context.Background(), "SELECT before::text, after::text, actor FROM config.history WHERE id = 3").Scan(&before, &after, &actor))
	assert.JSONEq(t, `{"argv":"[\"prog\",\"a b\"]","enabled":"true","every":"0","keepalive":"true","keys":"","machine":"m1","seat":""}`, before)
	assert.JSONEq(t, `{"argv":"[\"prog\",\"a b\"]","enabled":"false","every":"0","keepalive":"true","keys":"","machine":"m1","seat":"","reason":"why"}`, after)
	assert.Equal(t, "a1", actor)
	var cores *int
	require.NoError(t, c.QueryRow(context.Background(), "SELECT cores FROM config.machines WHERE name = 'm1'").Scan(&cores))
	require.NotNil(t, cores)
	assert.Equal(t, 16, *cores)

	for _, refusal := range [][]string{
		{"loop", "add", "r-x", "--machine", "nope", "--argv", `["p"]`, "--every", "5", "--actor", "a1"},
		{"machine", "remove", "m1", "--actor", "a1"},
		{"machine", "add", "m1", "--user", "u", "--actor", "a1"},
	} {
		code, _, errs := realRun(t, env, refusal...)
		assert.Equal(t, 1, code, "%v: %s", refusal, errs)
		assert.Contains(t, errs, "REFUSED")
	}

	code, out, errs := realRun(t, env, "inventory", "--host", "m1")
	require.Equal(t, 0, code, errs)
	for _, want := range []string{`"cores": 16`, `"nova_os": "linux"`, `"ansible_connection": "local"`, `"log": "~/nova-bench/loops/r-m1.log"`} {
		assert.Contains(t, out, want)
	}
	code, out, errs = realRun(t, env, "inventory", "--list")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, `"nova_pg_dsn": "postgres://nova@m1/postgres"`)

	assert.Equal(t, "CONFIG REMOVE kind=loop name=r-m1 rev=8\n", ok("loop", "remove", "r-m1", "--actor", "a1"))
}

// TestFunctionalOldColumns models the live store: main's schema, seeded the way
// the old tool wrote it, then the new schema.sql twice.
func TestFunctionalOldColumns(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t, "testdata/old-schema.sql")}
	ctx := context.Background()
	c, err := pgx.Connect(ctx, env["NOVA_PG_DSN"])
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	for _, q := range []string{
		`INSERT INTO config.machines (name, "user", seat, slots, width, tla, roles, gates_only) VALUES
		   ('old-store', 'nova', 'seat1', 8, 2, true, 'bench,coordination,services', 'g'), ('old-plain', 'nova', 'seat2', 4, NULL, false, 'bench', '')`,
		`INSERT INTO config.loops (name, machine, argv, seat, keepalive) VALUES ('old-loop', 'old-plain', '["prog"]', 'seat2', true)`,
		`UPDATE config.fleet SET store = 'old-store', coordinator = 'old-store', redis_port = 6380, pg_dsn = 'postgres://nova@old-store/x', bus = 'b', loops_dir = 'd'`,
	} {
		_, err = c.Exec(ctx, q)
		require.NoError(t, err, q)
	}
	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = c.Exec(ctx, string(schema))
		require.NoError(t, err, "schema.sql runs on the old store")
	}
	for _, args := range [][]string{{"machine", "list"}, {"machine", "show", "old-store"}, {"loop", "list"}, {"fleet", "show"}, {"inventory"}} {
		code, out, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
		assert.NotContains(t, out, "seat1", "%v", args)
	}
	code, out, errs := realRun(t, env, "machine", "show", "old-store")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "roles=bench,coordination,services")
	code, _, errs = realRun(t, env, "machine", "add", "new1", "--user", "u", "--actor", "a1")
	require.Equal(t, 0, code, "machine add on the old store: %s", errs)
	code, _, errs = realRun(t, env, "machine", "remove", "old-store", "--actor", "a1")
	require.Equal(t, 0, code, "machine remove of the old store/coordinator machine: %s", errs)
	code, _, errs = realRun(t, env, "machine", "set", "old-plain", "--cores", "8", "--actor", "a1")
	require.Equal(t, 0, code, errs)

	var seat string
	var slots int
	require.NoError(t, c.QueryRow(ctx, "SELECT seat, slots FROM config.machines WHERE name = 'old-plain'").Scan(&seat, &slots))
	assert.Equal(t, "seat2", seat)
	assert.Equal(t, 4, slots)
	var port int
	var dsn string
	require.NoError(t, c.QueryRow(ctx, "SELECT redis_port, pg_dsn FROM config.fleet").Scan(&port, &dsn))
	assert.Equal(t, 6380, port, "the old fleet columns keep their values")
	assert.Equal(t, "postgres://nova@old-store/x", dsn)
	var n int
	require.NoError(t, c.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'config' AND table_name = 'machines' AND column_name IN ('address', 'os', 'arch', 'cores')`).Scan(&n))
	assert.Equal(t, 4, n, "the four new columns exist exactly once")
	require.NoError(t, c.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'config' AND table_name = 'machines' AND column_name IN ('seat', 'slots', 'width', 'tla', 'gates_only')`).Scan(&n))
	assert.Equal(t, 5, n, "no old column is dropped")
}

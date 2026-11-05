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
	"strings"
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
func startPG(t *testing.T) string {
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
	schema, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	_, err = c.Exec(context.Background(), string(schema))
	require.NoError(t, err)
	_, err = c.Exec(context.Background(), string(schema))
	require.NoError(t, err, "schema.sql runs twice")
	return dsn
}

func startRedis(t *testing.T) string {
	t.Helper()
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not on PATH")
	}
	port := freePort(t)
	cmd := exec.Command(server, "--port", port, "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--requirepass", "pw")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: the server is ours and disposable
	addr := "127.0.0.1:" + port
	require.Eventually(t, func() bool { _, err := redisDo(addr, []string{"AUTH", "pw"}); return err == nil }, wait, 20*time.Millisecond)
	return addr
}

func redisDo(addr string, cmds ...[]string) ([]any, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }() // ignored: every reply has been read
	return pipeline(c, cmds)
}

func realRun(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var out, errs bytes.Buffer
	d := deps{getenv: func(k string) string { return env[k] }, hostname: func() (string, error) { return "m1", nil }, openPG: openPG, beats: redisBeats}
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
	assert.Equal(t, "CONFIG ADD kind=machine name=m1 rev=1\n", ok("machine", "add", "m1", "--user", "nova", "--seat", "s1", "--slots", "8", "--actor", "a1"))
	ok("loop", "add", "r-m1", "--machine", "m1", "--argv", `["prog","a b"]`, "--keepalive", "true", "--actor", "a1")
	assert.Equal(t, "CONFIG SET kind=loop name=r-m1 rev=3 changed=enabled\n", ok("loop", "set", "r-m1", "--enabled", "false", "--actor", "a1", "--reason", "why"))
	assert.Equal(t, `LOOP name=r-m1 machine=m1 argv=["prog","a\x20b"] seat=- keys=- every=0 keepalive=true enabled=false`+"\nCONFIG LIST kind=loop rows=1\n", ok("loop", "list"))
	assert.Equal(t, "MACHINE name=m1 user=nova seat=s1 slots=8 runners=0 roles=bench width=- gates_only=- tla=false note=-\nCONFIG LIST kind=machine rows=1\n", ok("machine", "list"))
	show := ok("machine", "show", "m1")
	assert.Regexp(t, `created=20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ updated=20\d\d-.*Z loops=r-m1\n$`, show)
	ok("machine", "set", "m1", "--width", "4", "--note", "a note", "--actor", "a1")
	ok("machine", "set", "m1", "--width", "default", "--actor", "a1")
	ok("fleet", "set", "--store", "m1", "--coordinator", "m1", "--redis_port", "6380", "--pg_dsn", "postgres://nova@m1/postgres", "--actor", "a1")
	ok("machine", "set", "m1", "--roles", "bench,coordination,services", "--actor", "a1")

	c, err := pgx.Connect(context.Background(), env["NOVA_PG_DSN"])
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }() // ignored: closing the check connection
	var before, after, actor string
	require.NoError(t, c.QueryRow(context.Background(), "SELECT before::text, after::text, actor FROM config.history WHERE id = 3").Scan(&before, &after, &actor))
	assert.JSONEq(t, `{"argv":"[\"prog\",\"a b\"]","enabled":"true","every":"0","keepalive":"true","keys":"","machine":"m1","seat":""}`, before)
	assert.JSONEq(t, `{"argv":"[\"prog\",\"a b\"]","enabled":"false","every":"0","keepalive":"true","keys":"","machine":"m1","seat":"","reason":"why"}`, after)
	assert.Equal(t, "a1", actor)
	var width *int
	require.NoError(t, c.QueryRow(context.Background(), "SELECT width FROM config.machines WHERE name = 'm1'").Scan(&width))
	assert.Nil(t, width, "width default is NULL")

	for _, refusal := range [][]string{
		{"loop", "add", "r-x", "--machine", "nope", "--argv", `["p"]`, "--every", "5", "--actor", "a1"},
		{"machine", "remove", "m1", "--actor", "a1"},
		{"machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "1", "--actor", "a1"},
	} {
		code, _, errs := realRun(t, env, refusal...)
		assert.Equal(t, 1, code, "%v: %s", refusal, errs)
		assert.Contains(t, errs, "REFUSED")
	}

	addr := startRedis(t)
	_, err = redisDo(addr, []string{"AUTH", "pw"}, []string{"HSET", "bench:m1:beat", "os", "linux", "arch", "amd64", "ncpu", "4", "at", "1"},
		[]string{"SET", "sprint:beat:m1", `{"at":"2026-11-05T00:00:00Z","cores":16}`})
	require.NoError(t, err)
	env["NOVA_SPRINT_REDIS"], env["NOVA_SPRINT_REDIS_PASSWORD_ENV"], env["RPW"] = addr, "RPW", "pw"
	code, out, errs := realRun(t, env, "inventory", "--host", "m1")
	require.Equal(t, 0, code, errs)
	assert.Empty(t, errs)
	for _, want := range []string{`"cores": 16`, `"slots_share": 8`, `"nova_os": "linux"`, `"ansible_connection": "local"`, `"log": "~/nova-bench/loops/r-m1.log"`} {
		assert.Contains(t, out, want)
	}
	env["RPW"] = "wrong"
	code, _, errs = realRun(t, env, "inventory")
	assert.Equal(t, 2, code)
	assert.True(t, strings.Contains(errs, "WRONGPASS") || strings.Contains(errs, "invalid"), errs)
	assert.NotContains(t, errs, "wrong ")

	assert.Equal(t, "CONFIG REMOVE kind=loop name=r-m1 rev=8\n", ok("loop", "remove", "r-m1", "--actor", "a1"))
}

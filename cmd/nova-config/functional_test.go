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

// pgEnv is the environment for initdb and postgres: the test's own, with
// LC_ALL=C added when no locale is set, since Homebrew postgres on macOS
// refuses to start without one ("postmaster became multithreaded").
func pgEnv() []string {
	if os.Getenv("LC_ALL") == "" && os.Getenv("LANG") == "" {
		return append(os.Environ(), "LC_ALL=C")
	}
	return os.Environ()
}

// stopPostgres asks the server for a fast shutdown, so it removes its System V
// shared memory segment: a SIGKILLed postmaster leaks it, and macOS allows only
// 32 (kern.sysv.shmmni) before every initdb fails. It kills after 10 seconds.
func stopPostgres(cmd *exec.Cmd) {
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }() // ignored: the server is ours and disposable
	_ = cmd.Process.Signal(os.Interrupt)        // ignored: it may have exited already
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill() // ignored: the server is ours and disposable
		<-done
	}
}

// startPG starts a PostgreSQL of this test's own on a free local port, makes
// the tables with schema.sql and returns its password-free DSN.
func startPG(t *testing.T, schemas ...string) string {
	t.Helper()
	bin, dir := pgBin(t), t.TempDir()
	initdb := exec.Command(filepath.Join(bin, "initdb"), "-D", filepath.Join(dir, "data"), "-U", "nova", "--auth=trust", "-E", "UTF8")
	initdb.Env = pgEnv()
	out, err := initdb.CombinedOutput()
	require.NoError(t, err, "%s", out)
	port := freePort(t)
	cmd := exec.Command(filepath.Join(bin, "postgres"), "-D", filepath.Join(dir, "data"), "-p", port, "-k", dir, "-c", "listen_addresses=127.0.0.1", "-c", "fsync=off")
	cmd.Env = pgEnv()
	logs, err := os.Create(filepath.Join(dir, "postgres.log"))
	require.NoError(t, err)
	defer func() { _ = logs.Close() }() // ignored: the child holds its own descriptor
	cmd.Stdout, cmd.Stderr = logs, logs
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { stopPostgres(cmd) })
	dsn := "postgres://nova@127.0.0.1:" + port + "/postgres?sslmode=disable"
	var c *pgx.Conn
	if !assert.Eventually(t, func() bool { c, err = pgx.Connect(context.Background(), dsn); return err == nil }, wait, 50*time.Millisecond) {
		log, _ := os.ReadFile(logs.Name()) // ignored: the log is a diagnostic
		t.Fatalf("postgres never answered: %v\npostgres said:\n%s", err, log)
	}
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
	d := deps{getenv: func(k string) string { return env[k] }, hostname: func() (string, error) { return "m1", nil }, now: time.Now, openPG: openPG}
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
	assert.Equal(t, "CONFIG ADD kind=machine name=m1 rev=1\nnext: nova-fleet join m1\n", ok("machine", "add", "m1", "--user", "nova", "--actor", "a1"))
	ok("service", "add", "r-m1", "--machine", "m1", "--argv", `["prog","a b"]`, "--keepalive", "true", "--actor", "a1")
	assert.Equal(t, "CONFIG SET kind=machine name=m1 changed=none rev=unchanged\n", ok("machine", "set", "m1", "--user", "nova", "--actor", "a1"), "a no-op set")
	assert.Equal(t, "CONFIG SET kind=service name=r-m1 rev=3 changed=enabled\nnext: nova-fleet services --check\n", ok("service", "set", "r-m1", "--enabled", "false", "--actor", "a1", "--reason", "why"))
	assert.Equal(t, `SERVICE name=r-m1 machine=m1 argv=["prog","a b"] seat=- keys=- every=0 keepalive=true enabled=false env={} log=-`+"\nCONFIG LIST kind=service rows=1\n", ok("service", "list"))
	assert.Equal(t, "MACHINE name=m1 address=- public=- user=nova os=- arch=- cores=- roles=bench runners=0 windows_runners=- runner_env={} note=-\nCONFIG LIST kind=machine rows=1\n", ok("machine", "list"))
	show := ok("machine", "show", "m1")
	assert.Regexp(t, `created=20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ updated=20\d\d-.*Z services=r-m1\n$`, show)
	ok("machine", "set", "m1", "--cores", "4", "--note", "a note", "--actor", "a1")
	ok("machine", "set", "m1", "--cores", "default", "--actor", "a1")
	ok("machine", "set", "m1", "--cores", "16", "--os", "linux", "--arch", "amd64", "--roles", "bench,services", "--actor", "a1")
	ok("machine", "set", "m1", "--public", "edge.example.com", "--actor", "a1")

	c, err := pgx.Connect(context.Background(), env["NOVA_PG_DSN"])
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }() // ignored: closing the check connection
	var before, after, actor string
	require.NoError(t, c.QueryRow(context.Background(), "SELECT before::text, after::text, actor FROM config.history WHERE id = 3").Scan(&before, &after, &actor))
	assert.JSONEq(t, `{"argv":"[\"prog\",\"a b\"]","enabled":"true","env":"{}","every":"0","keepalive":"true","keys":"","log":"","machine":"m1","seat":""}`, before)
	assert.JSONEq(t, `{"argv":"[\"prog\",\"a b\"]","enabled":"false","env":"{}","every":"0","keepalive":"true","keys":"","log":"","machine":"m1","seat":"","reason":"why"}`, after)
	assert.Equal(t, "a1", actor)
	var public *string
	require.NoError(t, c.QueryRow(context.Background(), "SELECT public FROM config.machines WHERE name = 'm1'").Scan(&public))
	require.NotNil(t, public)
	assert.Equal(t, "edge.example.com", *public)
	ok("machine", "set", "m1", "--public", "", "--actor", "a1")
	require.NoError(t, c.QueryRow(context.Background(), "SELECT public FROM config.machines WHERE name = 'm1'").Scan(&public))
	assert.Nil(t, public, "an unset public address is NULL")
	var cores *int
	require.NoError(t, c.QueryRow(context.Background(), "SELECT cores FROM config.machines WHERE name = 'm1'").Scan(&cores))
	require.NotNil(t, cores)
	assert.Equal(t, 16, *cores)

	for _, refusal := range []struct {
		args []string
		want string
	}{
		{[]string{"service", "add", "r-x", "--machine", "nope", "--argv", `["p"]`, "--every", "5", "--actor", "a1"}, "nova-config service add: no machine nope; the machines are m1; run: nova-config machine add nope --user <user>\n"},
		{[]string{"machine", "remove", "m1", "--actor", "a1"}, "nova-config machine remove: machine m1 still runs services r-m1; remove them first: nova-config service remove r-m1 --actor <you>\n"},
		{[]string{"machine", "add", "m1", "--user", "u", "--actor", "a1"}, "nova-config machine add: machine m1 exists; use: nova-config machine set m1\n"},
	} {
		code, _, errs := realRun(t, env, refusal.args...)
		assert.Equal(t, 1, code, "%v: %s", refusal.args, errs)
		assert.Equal(t, refusal.want, errs)
	}
	code, out, errs := realRun(t, env, "history", "service")
	require.Equal(t, 0, code, errs)
	assert.Regexp(t, `^HISTORY at=20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ actor=a1 kind=service name=r-m1 op=set rev=3\n  enabled: true -> false\n  reason: "why"\nHISTORY .* op=add rev=2\n`, out)
	assert.Contains(t, out, "CONFIG HISTORY rows=2\n")
	code, out, errs = realRun(t, env, "history", "--since", "1h")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "op=set rev=3")

	code, out, errs = realRun(t, env, "inventory", "--host", "m1")
	require.Equal(t, 0, code, errs)
	for _, want := range []string{`"cores": 16`, `"nova_os": "linux"`, `"ansible_connection": "local"`, `"__ansible_unsafe": "~/.local/state/nova/services/r-m1.log"`, `"env": {}`} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "nova_public", "the public address was unset")
	code, out, errs = realRun(t, env, "inventory", "--list")
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, out, "nova_pg_dsn")

	assert.Equal(t, "CONFIG REMOVE kind=service name=r-m1 rev=9\n", ok("service", "remove", "r-m1", "--actor", "a1"))
}

// A store the schema was never applied to says how to apply it, in one line.
func TestFunctionalNoSchema(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t, "testdata/empty.sql")}
	for _, args := range [][]string{{"machine", "list"}, {"history"}, {"inventory"}} {
		code, out, errs := realRun(t, env, args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Empty(t, out)
		assert.Contains(t, errs, `store has no current schema; run: nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"`)
		assert.Equal(t, 1, strings.Count(errs, "\n"), errs)
	}
}

// A store nothing listens at is "cannot reach", with the host and port. The
// cause is not pinned: a parallel test can take the freed port and hang up
// ("connect failed" instead of "connection refused"); TestCause holds the causes.
func TestFunctionalUnreachable(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	code, out, errs := realRun(t, map[string]string{"NOVA_PG_DSN": "postgres://nova@127.0.0.1:" + port + "/postgres?sslmode=disable"}, "machine", "list")
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assert.Regexp(t, `^nova-config machine list: cannot reach postgres at 127\.0\.0\.1:`+port+` \(NOVA_PG_DSN\): (connection refused|connect failed|timed out); check NOVA_PG_DSN or run: PGPASSWORD="\$NOVA_PG_PASSWORD" psql "\$NOVA_PG_DSN" -c 'select 1'\n$`, errs)
	assert.Equal(t, 1, strings.Count(errs, "\n"), errs)
}

func TestOpenPGConnectFailure(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0") // a port nothing answers on, once closed
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	_, err = openPG(t.Context(), "postgres://u@"+addr+"/x?sslmode=disable&connect_timeout=2", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot reach postgres at "+addr+" (NOVA_PG_DSN): ")
	assert.NotContains(t, err.Error(), "\n")
}

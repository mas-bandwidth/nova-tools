package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTable invokes the binary's own entry point.
func runTable(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// at appends --redis <addr> to a command line.
func at(addr string, args ...string) []string { return append(args, "--redis", addr) }

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runTable()
	require.EqualValues(t, 2, code, "bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Empty(t, stdout, "bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Equal(t, "TABLE REFUSED: no verb given; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help\n", stderr, "bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	code, stdout, _ = runTable("help")
	require.EqualValues(t, 0, code, "help: exit %d\n%s", code, stdout)
	require.True(t, strings.HasPrefix(stdout, "nova-table: "), "help: exit %d\n%s", code, stdout)
	require.Contains(t, stdout, "\nexample:\n", "help: exit %d\n%s", code, stdout)
	code, stdout, _ = runTable("version")
	require.EqualValues(t, 0, code, "version: exit %d %q", code, stdout)
	require.True(t, strings.HasPrefix(stdout, "nova-table "), "version: exit %d %q", code, stdout)
}

// TestMistakesAreRefusedWithTheWayForward: every mistake an AI makes is
// refused in the one grammar (`<VERB> REFUSED: <why>; run: <remedy>`), at
// exit 2, in one line naming only the word that was wrong, the names there are
// and the nearest, and the help of the verb the mistake was made in.
func TestMistakesAreRefusedWithTheWayForward(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown verb", []string{"craete", "--redis", "x"}, `TABLE REFUSED: unknown verb "craete"; did you mean create? the verbs are help, create,`},
		{"unknown verb of a group", []string{"row", "ad"}, `ROW REFUSED: unknown verb "row ad" in row; did you mean add? the verbs are add, set,`},
		{"bare group", []string{"cell"}, `CELL REFUSED: cell wants one of its verbs; the verbs are add, remove, move, members; run: nova-table help cell`},
		{"unknown flag", []string{"create", "t", "--colums", "a"}, `CREATE REFUSED: unknown flag --colums; the flags of create are `},
		{"bad value", []string{"create", "t", "--columns", "a", "--epoch", "bogus"}, `CREATE REFUSED: invalid value for --epoch: it wants a whole number of zero or more`},
		{"unknown help", []string{"help", "craete"}, `HELP REFUSED: unknown verb "craete"; did you mean create?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runTable(tc.args...)
			assert.EqualValues(t, 2, code)
			assert.Empty(t, stdout)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "stderr %q, want it to open %q", stderr, tc.want)
			assert.Equal(t, 1, strings.Count(stderr, "\n"), "one line: %q", stderr)
			assert.NotContains(t, stderr, "--redis x", "the refusal names only the word that was wrong: %q", stderr)
		})
	}
	for _, tc := range []struct {
		args   []string
		remedy string
	}{
		{[]string{"create", "t", "--columns", "a", "--epoch", "bogus"}, "; run: nova-table help create\n"},
		{[]string{"create", "t", "--colums", "a"}, "did you mean --columns?; run: nova-table help create\n"},
		{[]string{"cell", "add", "t"}, "; run: nova-table help cell add\n"},
	} {
		_, _, stderr := runTable(tc.args...)
		assert.True(t, strings.HasSuffix(stderr, tc.remedy), "%v: %q, want the verb's help %q", tc.args, stderr, tc.remedy)
	}
}

// TestLoginIsTheStoresLogin: with no seat, nova-table dials as nova-sprint's
// store did: no user is the default user with no password; a user with no
// password variable named reads NOVA_REDIS_BENCH_PASSWORD, named here since
// redisconn reads no default; a seat logs in as its user with the password
// its file holds, answered from memory under the seat's key.
func TestLoginIsTheStoresLogin(t *testing.T) {
	t.Parallel()
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	o, _, err := login("127.0.0.1:1", &seatcred.Selection{}, getenv)
	require.NoError(t, err, "no user: %v %v", o, err)
	require.Empty(t, o.User, "no user: %v %v", o, err)
	require.Empty(t, o.PasswordEnv, "no user: %v %v", o, err)
	require.Equal(t, "NOVA_SPRINT_REDIS_USER", o.Env.User, "no user: %v %v", o, err)
	require.Empty(t, o.Env.PasswordEnv, "no user: %v %v", o, err)
	// No user is no password, whatever the password's variable holds.
	env["NOVA_SPRINT_REDIS_PASSWORD_ENV"] = "OTHER_PASSWORD"
	{
		r, err := redisconn.Resolve(o, getenv)
		require.NoError(t, err, "no user, a password variable named: %v %v", r, err)
		require.Empty(t, r.PasswordEnv, "no user, a password variable named: %v %v", r, err)
	}
	delete(env, "NOVA_SPRINT_REDIS_PASSWORD_ENV")
	env["NOVA_SPRINT_REDIS_USER"] = "bench"
	{
		o, _, err = login("127.0.0.1:1", &seatcred.Selection{}, getenv)
		require.NoError(t, err, "default password variable: %v %v", o, err)
		require.Equal(t, "NOVA_REDIS_BENCH_PASSWORD", o.PasswordEnv, "default password variable: %v %v", o, err)
		require.Equal(t, "NOVA_SPRINT_REDIS_PASSWORD_ENV", o.Env.PasswordEnv, "default password variable: %v %v", o, err)
	}
	env["NOVA_SPRINT_REDIS_PASSWORD_ENV"] = "OTHER_PASSWORD"
	{
		o, _, err = login("127.0.0.1:1", &seatcred.Selection{}, getenv)
		require.NoError(t, err, "named password variable: %v %v", o, err)
		require.Empty(t, o.PasswordEnv, "named password variable: %v %v", o, err)
	}
	{
		r, err := redisconn.Resolve(o, getenv)
		require.Error(t, err, "named and empty: %v %v", r, err)
		require.Contains(t, err.Error(), "OTHER_PASSWORD is empty", "named and empty: %v %v", r, err)
	}

	var sel seatcred.Selection
	sel.SelectWith("synthetic", "", func(string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: "synthetic", User: "coordinator", Key: "NOVA_REDIS_COORDINATOR_PASSWORD", Password: secrets.NewSecret("synthetic-seat-pw")}, nil
	})
	o, seatenv, err := login("127.0.0.1:1", &sel, getenv)
	require.NoError(t, err, "seat: %v %v", o, err)
	require.Equal(t, "coordinator", o.User, "seat: %v %v", o, err)
	require.Equal(t, "NOVA_REDIS_COORDINATOR_PASSWORD", o.PasswordEnv, "seat: %v %v", o, err)
	require.Equal(t, "synthetic-seat-pw", seatenv("NOVA_REDIS_COORDINATOR_PASSWORD"), "seat: %v %v", o, err)
	require.Equal(t, "bench", seatenv("NOVA_SPRINT_REDIS_USER"), "seat: %v %v", o, err)
	require.NotContains(t, o.String(), "synthetic-seat-pw", "seat options show the password: %s", o)

	var broken seatcred.Selection
	broken.SelectWith("synthetic", "", func(string) (seatcred.Cred, error) { return seatcred.Cred{}, errors.New("seat synthetic: no file") })
	{
		_, _, err := login("127.0.0.1:1", &broken, getenv)
		require.Error(t, err, "seat refusal: %v", err)
		require.Equal(t, "seat synthetic: no file", err.Error(), "seat refusal: %v", err)
	}
}

// TestSeatWordsNameTheSeat: a seat's password is named as the seat's file's
// key, never as a variable of the environment; without a seat the words are
// redisconn's.
func TestSeatWordsNameTheSeat(t *testing.T) {
	t.Parallel()
	line := "redis at h:1 as user coordinator (password from NOVA_REDIS_COORDINATOR_PASSWORD): login refused: WRONGPASS; next: check that NOVA_REDIS_COORDINATOR_PASSWORD holds the password of coordinator and that the store has that user switched on"
	{
		got := seatWords(&seatcred.Selection{}, line)
		require.Equal(t, line, got, "no seat: %q", got)
	}
	var sel seatcred.Selection
	sel.SelectWith("synthetic", "", func(string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: "synthetic", User: "coordinator", Key: "NOVA_REDIS_COORDINATOR_PASSWORD", Password: secrets.NewSecret("synthetic-seat-pw")}, nil
	})
	want := "redis at h:1 as user coordinator (password from seat synthetic, key NOVA_REDIS_COORDINATOR_PASSWORD of its file): login refused: WRONGPASS; next: check that seat synthetic's file holds under NOVA_REDIS_COORDINATOR_PASSWORD the password of coordinator and that the store has that user switched on"
	{
		got := seatWords(&sel, line)
		require.Equal(t, want, got, "seat:\n got %q\nwant %q", got, want)
	}
	err := &reworded{want, errors.New("cause")}
	require.ErrorIs(t, err, err.err, "reworded: %v", err)
	require.Equal(t, want, err.Error(), "reworded: %v", err)
}

// TestNoStoreRefusalsNameTheFirstTry: a verb with no store to reach is
// refused naming what was tried and the way to a first try: the one command
// that starts a throwaway local store when redis-server is on PATH (quoted,
// printing the --redis to give), else that none is there and that a verb
// that writes runs with no store under --dry-run.
func TestNoStoreRefusalsNameTheFirstTry(t *testing.T) {
	t.Parallel()
	found := func(string) (string, error) { return "/opt/redis bin/redis-server", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	unreachable := "redis at /tmp/x.sock as the default user, no password: unreachable: dial unix /tmp/x.sock: connect: no such file or directory; next: start the store or correct the address, which was given to this tool"
	got := firstTry(unreachable, found)
	assert.True(t, strings.HasPrefix(got, unreachable+"; for a first try, start a throwaway store"), got)
	assert.True(t, strings.HasSuffix(got, `; run: d=$(mktemp -d) && '/opt/redis bin/redis-server' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock"`), got)
	got = firstTry(unreachable, missing)
	assert.Contains(t, got, "no redis-server is on PATH")
	assert.Contains(t, got, "under --dry-run")
	assert.True(t, strings.HasSuffix(got, "; run: nova-table help"), got)

	// no address at all: refused before any dial, with the same way forward
	for name, look := range map[string]func(string) (string, error){"on PATH": found, "not on PATH": missing} {
		app := &application{getenv: func(string) string { return "" }, lookPath: look}
		var out, errout bytes.Buffer
		code := app.dispatch([]string{"list", "--redis", ""}, &out, &errout)
		assert.EqualValues(t, 2, code, name)
		assert.Empty(t, out.String(), name)
		assert.True(t, strings.HasPrefix(errout.String(), "LIST REFUSED: --redis <addr> is required"), "%s: %q", name, errout.String())
		assert.Contains(t, errout.String(), "for a first try", name)
		assert.Equal(t, 1, strings.Count(errout.String(), "\n"), name)
	}
}

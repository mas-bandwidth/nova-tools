package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
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
	require.Equal(t, "nova-table: no verb; available: help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help\n", stderr, "bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	code, _, stderr = runTable("bogus")
	require.EqualValues(t, 2, code, "unknown verb: exit %d stderr %q", code, stderr)
	require.True(t, strings.HasPrefix(stderr, "nova-table: unknown verb bogus;"), "unknown verb: exit %d stderr %q", code, stderr)
	require.True(t, strings.HasSuffix(stderr, "; run: nova-table help\n"), "unknown verb: exit %d stderr %q", code, stderr)
	code, stdout, _ = runTable("help")
	require.EqualValues(t, 0, code, "help: exit %d\n%s", code, stdout)
	require.True(t, strings.HasPrefix(stdout, "nova-table: "), "help: exit %d\n%s", code, stdout)
	require.Contains(t, stdout, "\nexample:\n", "help: exit %d\n%s", code, stdout)
	code, stdout, _ = runTable("version")
	require.EqualValues(t, 0, code, "version: exit %d %q", code, stdout)
	require.True(t, strings.HasPrefix(stdout, "nova-table "), "version: exit %d %q", code, stdout)
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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// defaultServerApp is main's client app as main.go makes it: the seat login
// consulted, then the local sprint server the default when nothing names a store
// and nothing names a server (docs/SPEC-SPRINT.md, "The coordinator's verbs go to
// the server too"; docs/CLI.md, "nova-sprint").
func defaultServerApp(env map[string]string) *app {
	a := newApp(func(k string) string { return env[k] })
	a.seatLoginOn()
	a.serverDefaulted = a.useLocalServerDefault()
	return a
}

// With no NOVA_SPRINT_REDIS and no NOVA_SPRINT_SERVER set, a coordinator's verb
// reaches the local sprint server (127.0.0.1:6390) by default and asks for no
// secret: a cold coordinator runs nova-sprint <verb> and nothing else, with no
// wrapper and no Redis password.
func TestACoordinatorVerbReachesTheLocalServerWithNoSecret(t *testing.T) {
	t.Parallel()
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "NOVA_SPRINT_ACTOR": "boss"}
	a := defaultServerApp(env)
	t.Cleanup(a.close)
	require.True(t, a.serverDefaulted, "with no NOVA_SPRINT_REDIS and no NOVA_SPRINT_SERVER, the local sprint server is the default")
	a.loginSecret = func(secrets.Login) (secrets.Secret, error) {
		assert.Fail(t, "the verb asked the seat login for its password")
		return secrets.Secret{}, nil
	}
	var gotAddr string
	var sent [][]string
	a.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		gotAddr = addr
		sent = append(sent, verbs...)
		return []sprintwire.Result{{Code: 0, Stdout: "WHERE OK\n"}}, nil
	}
	var out, errb bytes.Buffer
	code := a.run([]string{"where"}, &out, &errb)
	require.Equal(t, 0, code, "%s%s", out.String(), errb.String())
	assert.Equal(t, LocalServer, gotAddr, "with no store and no server named, the verb reached the local sprint server")
	require.Len(t, sent, 1)
	assert.Equal(t, []string{"where", "--actor", "boss"}, sent[0], "the verb is sent as a client, with the caller's actor and no store")
	assert.Contains(t, out.String(), "WHERE OK")
	assert.Contains(t, errb.String(), "NOTE")
	assert.Contains(t, errb.String(), LocalServer, "the verb says which server it used")
}

// A server named in the environment is the one used, and the default's NOTE is
// not printed: nothing is implicit where the caller named the server.
func TestANamedServerIsUsedWithNoNote(t *testing.T) {
	t.Parallel()
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "NOVA_SPRINT_ACTOR": "boss", ServerEnv: "127.0.0.1:6399"}
	a := defaultServerApp(env)
	t.Cleanup(a.close)
	require.False(t, a.serverDefaulted, "NOVA_SPRINT_SERVER named is not the default")
	var gotAddr string
	a.forward = func(_ context.Context, addr string, _ ...[]string) ([]sprintwire.Result, error) {
		gotAddr = addr
		return []sprintwire.Result{{Code: 0, Stdout: "WHERE OK\n"}}, nil
	}
	var out, errb bytes.Buffer
	code := a.run([]string{"where"}, &out, &errb)
	require.Equal(t, 0, code, "%s%s", out.String(), errb.String())
	assert.Equal(t, "127.0.0.1:6399", gotAddr, "the named server is the one reached")
	assert.Empty(t, errb.String(), "a named server is not announced as a default")
}

// A store named in the environment is where the verb runs: --redis and its
// variables stay the way for the server itself and the twins, and the local
// server stands in for no process that named a store.
func TestANamedStoreIsNotReplacedByTheLocalServer(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR"} {
		env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "NOVA_SPRINT_ACTOR": "boss", k: "127.0.0.1:6379"}
		a := defaultServerApp(env)
		assert.Falsef(t, a.serverDefaulted, "%s names a store: the local server is not the default", k)
		assert.Emptyf(t, a.getenv(ServerEnv), "%s names a store: no server is named", k)
		sent := 0
		a.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
			sent += len(verbs)
			return nil, errors.New("must not be reached")
		}
		a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
			return nil, errors.New("the store named in the environment is what this verb opens")
		}
		var out, errb bytes.Buffer
		code := a.run([]string{"where"}, &out, &errb)
		require.NotEqual(t, 0, code, "%s names a store: the verb runs on it here: %s%s", k, out.String(), errb.String())
		assert.Zero(t, sent, "%s names a store: the verb is not sent to a server", k)
		assert.NotContains(t, errb.String(), "NOTE")
		a.close()
	}
}

// A store login recorded by seat login names a store all the same: the verbs run
// on it with the login, as they did before the default, which stands in only
// where no store is named anywhere.
func TestARecordedLoginIsNotReplacedByTheLocalServer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	login := storeLogin{Redis: "127.0.0.1:6379", User: "studio", Store: filepath.Join(dir, "secrets"), As: "studio", Key: filepath.Join(dir, "studio.key"), Sops: "/usr/bin/sops", Secret: "REDIS"}
	b, err := json.Marshal(login)
	require.NoError(t, err)
	path := filepath.Join(dir, "config", "nova-sprint", "login.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, b, 0o600))
	a := defaultServerApp(map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config")})
	t.Cleanup(a.close)
	assert.False(t, a.serverDefaulted, "a login recorded by seat login names the store: the local server is not the default")
	assert.Empty(t, a.getenv(ServerEnv), "a login recorded names the store, not a server")
}

// The seat's store login is this machine's: seat login and seat logout record
// and remove the login where they are typed, never on the server (the default
// notwithstanding), while a coordinator's read goes through the server with no
// secret asked.
func TestTheSeatLoginAndLogoutStayOnThisMachine(t *testing.T) {
	t.Parallel()
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "NOVA_SPRINT_ACTOR": "boss"}
	a := defaultServerApp(env)
	t.Cleanup(a.close)
	require.True(t, a.serverDefaulted)
	sent := 0
	a.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		sent += len(verbs)
		return nil, errors.New("the seat login is not the server's")
	}
	var out, errb bytes.Buffer
	code := a.run([]string{"seat", "logout"}, &out, &errb)
	assert.Equal(t, 0, code, "%s%s", out.String(), errb.String())
	assert.Zero(t, sent, "seat logout removes the login of the machine it is typed on, never the server's")
	assert.NotContains(t, errb.String(), "NOTE")
	out.Reset()
	errb.Reset()
	a.run([]string{"seat", "login", "--check"}, &out, &errb)
	assert.Zero(t, sent, "seat login records the login of the machine it is typed on, never the server's")
	assert.NotContains(t, errb.String(), "NOTE")
}

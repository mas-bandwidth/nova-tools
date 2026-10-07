package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// With no NOVA_SPRINT_REDIS and no NOVA_SPRINT_SERVER set, a coordinator's
// verb reaches the local sprint server (127.0.0.1:6390) by default and asks
// for no secret: a cold coordinator runs nova-sprint <verb> and nothing else.
// See docs/SPEC-SPRINT.md, "The coordinator's verbs go to the server too".
func TestACoordinatorVerbReachesTheLocalServerWithNoSecret(t *testing.T) {
	t.Parallel()
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "NOVA_SPRINT_ACTOR": "boss"}
	c := newApp(func(k string) string { return env[k] })
	t.Cleanup(c.close)
	// no seat login file (a test's app has none), so the sentinel answers empty and
	// the default server is what a fresh coordinator has
	c.loginSecret = func(secrets.Login) (secrets.Secret, error) {
		assert.Fail(t, "the verb asked the seat login for its password")
		return secrets.Secret{}, nil
	}
	var gotAddr string
	var sent [][]string
	c.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		gotAddr = addr
		sent = append(sent, verbs...)
		return []sprintwire.Result{{Code: 0, Stdout: "WHERE OK\n"}}, nil
	}
	var out, errb bytes.Buffer
	code := c.run([]string{"where"}, &out, &errb)
	require.Equal(t, 0, code, "%s%s", out.String(), errb.String())
	assert.Equal(t, LocalServer, gotAddr, "with no store and no server named, the local server is used")
	require.Len(t, sent, 1)
	assert.Equal(t, []string{"where", "--actor", "boss"}, sent[0])
	assert.Contains(t, out.String(), "WHERE OK")
	assert.Contains(t, errb.String(), LocalServer, "the verb says which it used")
}

// A server named in the environment is the one used, and the default's note is
// not printed (nothing is implicit).
func TestANamedServerIsUsedWithNoNote(t *testing.T) {
	t.Parallel()
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "NOVA_SPRINT_ACTOR": "boss", ServerEnv: "127.0.0.1:6390"}
	c := newApp(func(k string) string { return env[k] })
	t.Cleanup(c.close)
	c.loginSecret = func(secrets.Login) (secrets.Secret, error) {
		assert.Fail(t, "the verb asked the seat login for its password")
		return secrets.Secret{}, nil
	}
	c.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		return []sprintwire.Result{{Code: 0, Stdout: "WHERE OK\n"}}, nil
	}
	var out, errb bytes.Buffer
	code := c.run([]string{"where"}, &out, &errb)
	require.Equal(t, 0, code, "%s%s", out.String(), errb.String())
	assert.Empty(t, errb.String())
}

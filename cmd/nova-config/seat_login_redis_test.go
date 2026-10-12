package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nova-config-reads-the-seat-login: sprintCreds and redisAddress resolve the
// recorded nova-sprint seat login (the address, the ACL user and the password
// read in this process through the secrets reader, never into an environment
// or an output), and status names the seat login as the source. The real
// connection over a private Redis is pinned by the functional test
// TestNovaConfigReadsTheSeatLogin (seat_login_redis_functional_test.go).
func TestSeatLoginSprintCredsAndStatusSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	login := filepath.Join(dir, "nova-sprint", "login.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(login), 0o700))
	require.NoError(t, os.WriteFile(login, []byte(`{"redis":"127.0.0.1:6381","user":"coordinator","store":"/s","as":"studio","key":"/k/studio.key","sops":"/bin/sops","secret":"NOVA_REDIS_COORDINATOR_PASSWORD"}`), 0o600))
	const pw = "Pw4Sprint9"
	var asked []secrets.Login
	loginReaders.Store(login, loginReader(func(l secrets.Login) (secrets.Secret, error) {
		asked = append(asked, l)
		return secrets.NewSecret(pw), nil
	}))
	t.Cleanup(func() { loginReaders.Delete(login) })

	getenv := func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME":
			return dir
		}
		return ""
	}

	// the sprint store's login: the recorded user, the password read in this
	// process through the fake reader.
	user, password, err := sprintCreds("127.0.0.1:6381", getenv)
	require.NoError(t, err)
	assert.Equal(t, "coordinator", user, "the recorded ACL user")
	assert.Equal(t, pw, password, "the password read in process")
	assert.Equal(t, []secrets.Login{{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Name: "NOVA_REDIS_COORDINATOR_PASSWORD"}}, asked)

	// a different address is not the recorded login: the default user.
	user, password, err = sprintCreds("127.0.0.1:6382", getenv)
	require.NoError(t, err)
	assert.Equal(t, "", user, "not the recorded address: default user")
	assert.Equal(t, "", password, "not the recorded address: no password")

	// the address falls back to the recorded login, source seat-login.
	addr, source, err := redisAddressFrom("", getenv)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:6381", addr, "the recorded address")
	assert.Equal(t, "seat-login", source, "status names the seat login as the source")

	// status names the seat login as the source, and the password is in no
	// output and in no environment.
	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = dir
	h.env["NOVA_PG_DSN"] = dsn
	_, out, errs := h.run(t, "status")
	assert.Contains(t, out, " redis=127.0.0.1:6381 redis-from=seat-login", "status source: %q %q", out, errs)
	assert.NotContains(t, out+errs, pw, "the password is never printed")
	assert.NotContains(t, getenv("NOVA_SPRINT_REDIS")+getenv("NOVA_SPRINT_REDIS_USER"), pw, "never in an environment")
}

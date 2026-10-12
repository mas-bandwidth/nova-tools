//go:build functional

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// nova-config-reads-the-seat-login: a bare verb opens the sprint store over a
// private Redis with no NOVA_SPRINT_REDIS* set, from the recorded nova-sprint
// seat login: the address, the ACL user and the password read in this process
// through the secrets reader, never into an environment or an output. The
// store's default user is off, so only a connection as the recorded user (with
// the in-process password) answers; a wrong or absent login is refused. The
// verb runs through realDepsWith, so the production openRedis (pkg/redisconn)
// is the wiring under test, not a copy.
func TestNovaConfigReadsTheSeatLogin(t *testing.T) {
	t.Parallel()

	const pw = "Pw4Sprint9"
	addr := testutil.Start(t, "--user", "default", "off",
		"--user", "coordinator", "on", ">"+pw, "~*", "&*", "+@all")

	// The nova-sprint seat login record, in a scrubbed HOME/XDG_CONFIG_HOME.
	dir := t.TempDir()
	login := filepath.Join(dir, "nova-sprint", "login.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(login), 0o700))
	require.NoError(t, os.WriteFile(login, []byte(fmt.Sprintf(
		`{"redis":%q,"user":"coordinator","store":%q,"as":%q,"key":%q,"sops":%q,"secret":%q}`,
		addr, "/s", "studio", "/k/studio.key", "/bin/sops", "NOVA_REDIS_COORDINATOR_PASSWORD")), 0o600))

	var asked []secrets.Login
	loginReaders.Store(login, loginReader(func(l secrets.Login) (secrets.Secret, error) {
		asked = append(asked, l)
		return secrets.NewSecret(pw), nil
	}))
	t.Cleanup(func() { loginReaders.Delete(login) })

	// The scrubbed environment: XDG_CONFIG_HOME names the temp dir, and no
	// NOVA_* variable is set, so the sprint store comes only from the record.
	getenv := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	}
	d := realDepsWith(getenv)

	// The login the sprint store opens with: the recorded user, the password
	// read in this process through the fake reader.
	user, password, err := sprintCreds(addr, getenv)
	require.NoError(t, err)
	assert.Equal(t, "coordinator", user, "the recorded ACL user")
	assert.Equal(t, pw, password, "the password read in process")
	assert.Equal(t, []secrets.Login{{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Name: "NOVA_REDIS_COORDINATOR_PASSWORD"}}, asked)

	// A bare status opens the sprint store with no --redis and no
	// NOVA_SPRINT_REDIS*: it connects as the recorded user (the default user is
	// off, so any other login is refused), names the seat login as the source,
	// and keeps the password out of every output and out of the environment a
	// child inherits.
	file := filepath.Join(dir, "try.json")
	var out, errb bytes.Buffer
	require.Equal(t, 0, run([]string{"migrate", "--file", file}, &out, &errb, d), "migrate: %s %s", out.String(), errb.String())
	out.Reset()
	errb.Reset()
	require.Equal(t, 0, run([]string{"status", "--file", file}, &out, &errb, d), "status: %s %s", out.String(), errb.String())
	assert.Contains(t, out.String(), " redis-from=seat-login", "status names the seat login as the source")
	assert.NotContains(t, out.String()+errb.String(), pw, "the password is in no output")
	assert.NotContains(t, strings.Join(os.Environ(), "\n"), pw, "the password is in no environment a child inherits")
}

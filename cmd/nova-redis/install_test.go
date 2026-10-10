package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/units"
)

// install store and install bus write a unit that runs nova-redis serve itself, with
// the binding, the port, the store directory under the bench root and the login serve
// reads its password from, for macOS and Linux, into a fake home, loaded by the
// test's loader; uninstall unloads and removes it; the unit carries no password and
// no nova-secrets exec (card every-unit-installed-by-a-verb).
func TestInstallStoreAndBusWriteAUnitThatRunsServeWithItsLogin(t *testing.T) {
	t.Parallel()
	login := []string{"--secrets", "/srv/secrets", "--as", "studio", "--key", "/srv/studio.key", "--sops", "/usr/bin/sops", "--secret", "NOVA_REDIS_ADMIN_PASSWORD"}
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			h := newServeHarness(t, "")
			home := t.TempDir()
			var calls []string
			h.d.goos = goos
			h.d.home = func() (string, error) { return home, nil }
			h.d.executable = func() (string, error) { return "/opt/nova/bin/nova-redis", nil }
			h.d.loadUnit = func(_, op, p string) error { calls = append(calls, op+" "+filepath.Base(p)); return nil }
			unitDir := units.Dir(goos, home, func(string) string { return "" })
			for _, tc := range []struct{ kind, port string }{{"store", "6380"}, {"bus", "6381"}} {
				k, ok := units.UnitKindOf(tc.kind)
				require.True(t, ok)
				path := filepath.Join(unitDir, k.File(goos))

				code, out, errs := h.run(append([]string{"install", tc.kind, "--dry-run"}, login...)...)
				require.Equal(t, 0, code, errs)
				assert.Contains(t, out, "INSTALL "+strings.ToUpper(tc.kind)+" DRY-RUN unit="+path)
				assert.NoFileExists(t, path)

				code, out, errs = h.run(append([]string{"install", tc.kind}, login...)...)
				require.Equal(t, 0, code, errs)
				assert.Contains(t, out, "INSTALL "+strings.ToUpper(tc.kind)+" OK unit="+path+" written=true loaded=true")
				b, err := os.ReadFile(path)
				require.NoError(t, err)
				args, err := units.UnitArgs(goos, b)
				require.NoError(t, err)
				assert.Equal(t, append([]string{"/opt/nova/bin/nova-redis", "serve", "--bind", "127.0.0.1", "--port", tc.port,
					"--dir", filepath.Join(home, "nova-bench", "redis", tc.kind)}, login...), args)
				assert.NotContains(t, string(b), "nova-secrets")

				states, err := units.CheckUnits(unitDir, goos, []units.UnitKind{k})
				require.NoError(t, err)
				assert.Equal(t, units.UnitInstalled, states[0].State, states[0].Why)

				code, out, errs = h.run("uninstall", tc.kind)
				require.Equal(t, 0, code, errs)
				assert.Contains(t, out, "UNINSTALL "+strings.ToUpper(tc.kind)+" OK unit="+path+" removed=true")
				assert.NoFileExists(t, path)
			}
			store, _ := units.UnitKindOf("store")
			bus, _ := units.UnitKindOf("bus")
			assert.Equal(t, []string{"load " + store.File(goos), "unload " + store.File(goos), "load " + bus.File(goos), "unload " + bus.File(goos)}, calls)

			// the unit carries no password: a login missing a field is refused, nothing written
			code, _, errs := h.run("install", "store", "--secret", "NOVA_REDIS_ADMIN_PASSWORD")
			assert.Equal(t, 2, code)
			assert.Contains(t, errs, "it names no --secrets, --as, --key, --sops")
			code, _, errs = h.run(append([]string{"install", "store", "--bind", "0.0.0.0"}, login...)...)
			assert.Equal(t, 2, code)
			assert.Contains(t, errs, "binds every interface")
			assert.Len(t, calls, 4, "a refused install loads nothing")
		})
	}
}

// serve reads its password in its own process from the login its flags name, hands it
// to redis-server on stdin only, and refuses a login with a field missing or one that
// does not resolve; with no login it reads NOVA_REDIS_PASSWORD as before.
func TestServeReadsItsPasswordFromTheLoginItNames(t *testing.T) {
	t.Parallel()
	h := newServeHarness(t, "")
	var asked []secrets.Login
	h.d.readLogin = func(l secrets.Login) (secrets.Secret, error) {
		asked = append(asked, l)
		if l.Name == "GONE" {
			return secrets.Secret{}, errors.New("seat studio holds no GONE")
		}
		return secrets.NewSecret("pw-from-login"), nil
	}
	base := []string{"serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir}
	login := []string{"--secrets", "/srv/secrets", "--as", "studio", "--key", "/srv/studio.key", "--sops", "/usr/bin/sops", "--secret", "ADMIN"}

	code, out, errs := h.run(append(base, login...)...)
	require.Equal(t, 0, code, errs)
	require.Len(t, h.launches, 1)
	assert.Contains(t, string(h.launches[0].Config), `requirepass "pw-from-login"`)
	for _, e := range h.launches[0].Env {
		assert.NotContains(t, e, "pw-from-login")
	}
	assert.NotContains(t, out+errs, "pw-from-login")
	assert.Equal(t, []secrets.Login{{Store: "/srv/secrets", As: "studio", Key: "/srv/studio.key", Sops: "/usr/bin/sops", Name: "ADMIN"}}, asked)

	code, _, errs = h.run(append(base, "--secret", "ADMIN")...)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "it names no --secrets, --as, --key, --sops")
	code, _, errs = h.run(append(base, append(login[:len(login)-1], "GONE")...)...)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "does not resolve")
	code, _, errs = h.run(base...)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, PasswordEnv+" is empty")
	assert.Len(t, h.launches, 1, "a refused serve launches nothing")
}

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// coordinator-config-through-the-seat: a cold coordinator types nova-config --seat
// <name> on a read verb, with no DSN and no password variable in its environment: the
// row nova-sprint seat install wrote gives the DSN, and the password is read in process
// from the nova-secrets seat nova-sprint's store login names, under the row's variable
// name. The variable set wins; a seat that does not give it is refused with the remedy.
func TestSeatOnAReadVerbReadsThePasswordThroughTheStoreLogin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nova-config"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-config", "seats.tsv"), []byte("studio\t"+dsn+"\tNOVA_PG_CONFIG_PASSWORD\n"), 0o600))

	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = dir
	h.machine(t, "m1", "4")
	var opened []string
	d := h.deps()
	open := d.openStore
	d.openStore = func(ctx context.Context, s string) (pgStore, error) { opened = append(opened, s); return open(ctx, s) }
	do := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb, d)
		return code, out.String(), errb.String()
	}

	// no store login: refused, naming the variable and both remedies
	code, out, errs := do("machine", "list", "--seat", "studio")
	assert.Equal(t, 2, code, out)
	assert.Contains(t, errs, "seat studio: NOVA_PG_CONFIG_PASSWORD is not set and no store login is recorded at "+filepath.Join(dir, "nova-sprint", "login.json"))
	assert.Contains(t, errs, "nova-sprint seat login")
	assert.Empty(t, opened, "no store is opened without its password")

	// the store login nova-sprint seat login records: its seat gives the password
	login := filepath.Join(dir, "nova-sprint", "login.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(login), 0o700))
	require.NoError(t, os.WriteFile(login, []byte(`{"redis":"127.0.0.1:6381","user":"coordinator","store":"/s","as":"studio","key":"/k/studio.key","sops":"/bin/sops","secret":"NOVA_REDIS_COORDINATOR_PASSWORD"}`), 0o600))
	var asked []secrets.Login
	loginReaders.Store(login, loginReader(func(l secrets.Login) (secrets.Secret, error) {
		asked = append(asked, l)
		if l.Name != "NOVA_PG_CONFIG_PASSWORD" {
			return secrets.Secret{}, errors.New("no " + l.Name)
		}
		return secrets.NewSecret("Pw4Config9"), nil
	}))
	t.Cleanup(func() { loginReaders.Delete(login) })
	code, out, errs = do("machine", "list", "--seat", "studio")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "m1")
	assert.NotContains(t, out+errs, "Pw4Config9", "the password is never printed")
	require.Len(t, opened, 1)
	assert.Contains(t, opened[0], "nova_config:Pw4Config9@127.0.0.1:5432/nova", "the store is opened with the password the seat gave")
	assert.Equal(t, []secrets.Login{{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Name: "NOVA_PG_CONFIG_PASSWORD"}}, asked)

	// NOVA_SEAT in place of --seat, on another read verb
	h.env["NOVA_SEAT"] = "studio"
	code, _, errs = do("machine", "width", "m1")
	require.Equal(t, 0, code, errs)
	delete(h.env, "NOVA_SEAT")

	// the variable set wins: the seat is not asked
	h.env["NOVA_PG_CONFIG_PASSWORD"] = "FromEnv7"
	asked, opened = nil, nil
	code, _, errs = do("machine", "list", "--seat", "studio")
	require.Equal(t, 0, code, errs)
	assert.Empty(t, asked)
	require.Len(t, opened, 1)
	assert.Contains(t, opened[0], "nova_config:FromEnv7@")
	delete(h.env, "NOVA_PG_CONFIG_PASSWORD")

	// a seat that does not hold the key: refused with the seal remedy
	loginReaders.Store(login, loginReader(func(l secrets.Login) (secrets.Secret, error) {
		return secrets.Secret{}, errors.New("seat studio holds no " + l.Name)
	}))
	code, _, errs = do("machine", "list", "--seat", "studio")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "seal it with nova-secrets seal --as studio --name NOVA_PG_CONFIG_PASSWORD")
}

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envSeam returns a getenv seam backed by the map, so no test touches the
// real environment.
func envSeam(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

// TestDsnCoverResolveDSNRefusesMissingDSN: with no flag value and no
// NOVA_PG_DSN the resolution is refused, naming the flag and the variable.
func TestDsnCoverResolveDSNRefusesMissingDSN(t *testing.T) {
	t.Parallel()
	_, err := ResolveDSN("", envSeam(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--pg is required")
	assert.Contains(t, err.Error(), EnvPG)
}

// TestDsnCoverResolveDSNRefusesUnparsableDSN: a DSN neither spelling parses
// is refused with the parse error and the wanted shape.
func TestDsnCoverResolveDSNRefusesUnparsableDSN(t *testing.T) {
	t.Parallel()
	_, err := ResolveDSN("not a dsn", envSeam(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--pg:")
	assert.Contains(t, err.Error(), "postgres://user@host:5432/nova")
}

// TestDsnCoverResolveDSNRefusesFlagCarryingPassword: a URL DSN on the flag
// line that carries a password is refused, a ps reading the line.
func TestDsnCoverResolveDSNRefusesFlagCarryingPassword(t *testing.T) {
	t.Parallel()
	_, err := ResolveDSN("postgres://bob:sekrit@nova:5432/nova", envSeam(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--pg carries a password")
	assert.Contains(t, err.Error(), EnvPGPassEnv)
}

// TestDsnCoverResolveDSNRefusesEmptyNamedPasswordVariable: a named password
// variable that is empty is refused with its name.
func TestDsnCoverResolveDSNRefusesEmptyNamedPasswordVariable(t *testing.T) {
	t.Parallel()
	env := map[string]string{EnvPGPassEnv: "NOVA_TEST_PW"}
	_, err := ResolveDSN("postgres://bob@nova:5432/nova", envSeam(env))
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvPGPassEnv+"=NOVA_TEST_PW")
	assert.Contains(t, err.Error(), "is empty")
}

// TestDsnCoverResolveDSNFlagWithoutPasswordStands: a flag DSN without a
// password and no password variable anywhere is returned unchanged.
func TestDsnCoverResolveDSNFlagWithoutPasswordStands(t *testing.T) {
	t.Parallel()
	dsn, err := ResolveDSN("postgres://bob@nova:5432/nova", envSeam(nil))
	require.NoError(t, err)
	assert.Equal(t, "postgres://bob@nova:5432/nova", dsn)
}

// TestDsnCoverResolveDSNMissingFlagReadsEnvDSN: the DSN comes from
// NOVA_PG_DSN when the flag is empty.
func TestDsnCoverResolveDSNMissingFlagReadsEnvDSN(t *testing.T) {
	t.Parallel()
	env := map[string]string{EnvPG: "postgres://bob@nova:5432/nova", DefaultPassEnv: "pw"}
	dsn, err := ResolveDSN("", envSeam(env))
	require.NoError(t, err)
	assert.Equal(t, "postgres://bob:pw@nova:5432/nova", dsn)
}

// TestDsnCoverResolveDSNAppendsPasswordFromDefaultVariable: a DSN without a
// password takes it from NOVA_PG_PASSWORD and the password goes into the DSN
// in memory, the URL spelling kept.
func TestDsnCoverResolveDSNAppendsPasswordFromDefaultVariable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"url", "postgres://bob@nova:5432/nova", "postgres://bob:pw@nova:5432/nova"},
		{"keyword", "host=nova", "host=nova password='pw'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{DefaultPassEnv: "pw"}
			dsn, err := ResolveDSN(tc.dsn, envSeam(env))
			require.NoError(t, err)
			assert.Equal(t, tc.want, dsn)
		})
	}
}

// TestDsnCoverWithPasswordURLForm: the URL spelling takes the password with
// the user named in the DSN, or the user argument when the DSN names none.
func TestDsnCoverWithPasswordURLForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dsn  string
		user string
		want string
	}{
		{"user in dsn", "postgres://bob@nova:5432/nova", "", "postgres://bob:pw@nova:5432/nova"},
		{"user argument", "postgres://nova:5432/nova", "carol", "postgres://carol:pw@nova:5432/nova"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn, err := withPassword(tc.dsn, tc.user, "pw")
			require.NoError(t, err)
			assert.Equal(t, tc.want, dsn)
		})
	}
}

// TestDsnCoverWithPasswordKeywordFormEscapes: the keyword spelling appends
// password='…', escaping backslashes and quotes.
func TestDsnCoverWithPasswordKeywordFormEscapes(t *testing.T) {
	t.Parallel()
	dsn, err := withPassword("host=nova", "bob", `p'w\`)
	require.NoError(t, err)
	assert.Equal(t, `host=nova password='p\'w\\'`, dsn)
}

// TestDsnCoverWithPasswordRefusesUnparsableURL: a URL spelling that does not
// parse is refused with the parse error.
func TestDsnCoverWithPasswordRefusesUnparsableURL(t *testing.T) {
	t.Parallel()
	_, err := withPassword("postgres://%zz", "bob", "pw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--pg:")
}

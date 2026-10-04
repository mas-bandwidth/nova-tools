package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveDSNRefusesEveryFlagPassword pins the boundary the store's
// connecting contract promises (docs/nova-config/README.md, "Connecting"):
// a --pg flag that carries a password is refused in every spelling the
// pgconn parser accepts, because the flag line is where a ps reads it,
// while a DSN from the environment and a password injected from the
// password variable keep working.
func TestResolveDSNRefusesEveryFlagPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		flag   string
		env    map[string]string
		refuse bool
		want   string // the resolved DSN when the flag carries no password
	}{
		{name: "url userinfo password", flag: "postgres://store:synthetic-secret@db.invalid:5432/nova", refuse: true},
		{name: "url query password", flag: "postgres://store@db.invalid:5432/nova?password=synthetic-secret", refuse: true},
		{name: "url query percent-encoded password key", flag: "postgres://store@db.invalid:5432/nova?pass%77ord=synthetic-secret", refuse: true},
		{name: "url query mixed-case encoded password key", flag: "postgres://store@db.invalid:5432/nova?%70aSsWoRd=synthetic-secret", refuse: true},
		{name: "url query password key with trailing blank", flag: "postgres://store@db.invalid:5432/nova?password =synthetic-secret", refuse: true},
		{name: "url query password key with leading blank", flag: "postgres://store@db.invalid:5432/nova? password=synthetic-secret", refuse: true},
		{name: "keyword password", flag: "host=db.invalid user=store port=5432 password=synthetic-secret", refuse: true},
		{name: "keyword quoted password", flag: "host=db.invalid port=5432 password='synthetic-secret'", refuse: true},
		{name: "keyword quoted escaped password", flag: `host=db.invalid password='synth\'etic-secret'`, refuse: true},
		{name: "url explicit empty password", flag: "postgres://store:@db.invalid:5432/nova", refuse: true},
		{name: "url query explicit empty password", flag: "postgres://store@db.invalid:5432/nova?password=", refuse: true},
		{name: "keyword quoted explicit empty password", flag: "host=db.invalid port=5432 password=''", refuse: true},
		{name: "keyword bare explicit empty password", flag: "host=db.invalid port=5432 password=", refuse: true},
		{name: "malformed keyword carrying a password", flag: "host=db.invalid password='synthetic-secret", refuse: true},
		{name: "malformed encoded password key never echoes the dsn", flag: "postgres://store@db.invalid:5432/nova?%70assword=synthetic-secret%zz", refuse: true},
		{name: "malformed keyword never echoes the dsn", flag: `pass\ word=synthetic-secret`, refuse: true},
		{
			name: "environment dsn with a password is kept",
			flag: "",
			env:  map[string]string{EnvPG: "postgres://store:synthetic-secret@db.invalid:5432/nova"},
			want: "postgres://store:synthetic-secret@db.invalid:5432/nova",
		},
		{
			name: "url flag without a password takes the injected one",
			flag: "postgres://store@db.invalid:5432/nova",
			env:  map[string]string{DefaultPassEnv: "synthetic-secret"},
			want: "postgres://store:synthetic-secret@db.invalid:5432/nova",
		},
		{
			name: "keyword flag without a password takes the injected one",
			flag: "host=db.invalid user=store port=5432",
			env:  map[string]string{DefaultPassEnv: "synthetic-secret"},
			want: "host=db.invalid user=store port=5432 password='synthetic-secret'",
		},
		{
			name: "keyword value quoting the word password is not a password field",
			flag: `options='password=x' host=db.invalid user=store`,
			env:  map[string]string{DefaultPassEnv: "synthetic-secret"},
			want: `options='password=x' host=db.invalid user=store password='synthetic-secret'`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, envOf(tc.env))
			if tc.refuse {
				require.Error(t, err, "a flag DSN carrying a password is refused")
				assert.ErrorContains(t, err, "carries a password")
				assert.ErrorContains(t, err, EnvPGPassEnv, "the refusal names the password variable as the remedy")
				assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the secret")
				assert.NotContains(t, err.Error(), tc.flag, "the refusal never echoes the DSN")
				return
			}
			require.NoError(t, err, "a DSN with no flag password resolves")
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveDSNRefusesAFlagPasswordBesideAControlByte pins the parsed-side
// boundary the lexical check cannot reach: net/url refuses a flag carrying a
// control byte, so flagCarriesPassword's URL branch never sees its query,
// while the pgconn URI reader trims only a literal space around a query key
// and treats other bytes as data, parses the flag, and sets a password from
// it. A flag whose parsed config carries a non-empty Password is refused with
// refuseFlagPassword's text whatever the lexical check said, and neither the
// flag nor its secret is ever returned or echoed
// (docs/nova-config/README.md, "Connecting").
func TestResolveDSNRefusesAFlagPasswordBesideAControlByte(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		flag string
	}{
		{name: "control byte beside a password query key", flag: "postgres://store@db.invalid:5432/nova? password=synthetic-secret&application_name=a\tb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, envOf(nil))
			require.Error(t, err, "a flag whose parsed config carries a password is refused")
			assert.Equal(t, refuseFlagPassword().Error(), err.Error(), "the refusal is refuseFlagPassword's own text")
			assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the secret")
			assert.NotContains(t, err.Error(), tc.flag, "the refusal never echoes the DSN")
			assert.NotEqual(t, tc.flag, got, "the flag text is never returned")
			assert.Empty(t, got, "nothing resolves when the flag is refused")
		})
	}
}

// TestResolveDSNEnvParseErrorNeverEchoesTheDSN pins the environment path's
// parse refusal (docs/nova-config/README.md, "Connecting"): with an empty
// flag and a NOVA_PG_DSN the pgconn parser cannot read, the error names the
// variable and the wanted shape and quotes neither the DSN nor its password.
// pgconn's own error text runs the raw connection string through a
// best-effort redactor that malformed input defeats, so a DSN that is not on
// a command line still must not have its text echoed back.
func TestResolveDSNEnvParseErrorNeverEchoesTheDSN(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		dsn  string
	}{
		{name: "uri with a non-numeric port", dsn: "postgres://store:synthetic-secret@db.invalid:notaport/nova"},
		{name: "keyword with spaces around the equals and an unclosed quote", dsn: "password = 'synthetic-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveDSN("", envOf(map[string]string{EnvPG: tc.dsn}))
			require.Error(t, err, "an unparsable environment DSN is refused")
			assert.Contains(t, err.Error(), EnvPG, "the refusal names the variable it could not read")
			assert.Contains(t, err.Error(), "postgres://user@host:5432/nova", "the refusal carries the wanted shape")
			assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the password")
			assert.NotContains(t, err.Error(), tc.dsn, "the refusal never echoes the DSN text")
		})
	}
}

// TestResolveDSNRefusesAFlagSSLPassword pins the boundary the store's
// connecting contract promises (docs/nova-config/README.md, "Connecting"):
// sslpassword is the passphrase for the SSL client key, a secret the pgconn
// parser reads from the flag, so a --pg flag that carries one in either
// spelling the parser accepts is refused with refuseFlagPassword's own text,
// which quotes neither the flag nor its secret.
func TestResolveDSNRefusesAFlagSSLPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		flag string
	}{
		{name: "url query sslpassword", flag: "postgres://store@db.invalid:5432/nova?sslpassword=synthetic-secret"},
		{name: "keyword sslpassword", flag: "sslpassword=synthetic-secret host=db.invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, envOf(nil))
			require.Error(t, err, "a flag DSN carrying an sslpassword is refused")
			assert.Equal(t, refuseFlagPassword().Error(), err.Error(), "the refusal is refuseFlagPassword's own text")
			assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the secret")
			assert.NotContains(t, err.Error(), tc.flag, "the refusal never echoes the DSN")
			assert.Empty(t, got, "nothing resolves when the flag is refused")
		})
	}
}

// envOf is the getenv ResolveDSN reads: one lookup into the case's
// variables, so a test never touches the process environment.
func envOf(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

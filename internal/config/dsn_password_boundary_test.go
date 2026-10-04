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
		{name: "url query password key with trailing space", flag: "postgres://store@db.invalid:5432/nova?password =synthetic-secret", refuse: true},
		{name: "url query password key with leading space", flag: "postgres://store@db.invalid:5432/nova? password=synthetic-secret", refuse: true},
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

// envOf is the getenv ResolveDSN reads: one lookup into the case's
// variables, so a test never touches the process environment.
func envOf(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

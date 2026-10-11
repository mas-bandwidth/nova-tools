package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveDSNRefusesEveryFlagPasswordSpelling pins the rule that a --pg flag
// carrying a password in any libpq spelling is refused: the DSN would reach the
// command line, where a ps reads it (docs/SPEC-CONFIG.md, "Connecting";
// pkg/config/dsn.go, ResolveDSN). pgconn reads every spelling into
// cfg.Password, so one check covers the URL userinfo, the URI ?password= query
// and the keyword password= spellings (plain, percent-encoded and
// whitespace-padded keys), and the refusal never echoes the password.
func TestResolveDSNRefusesEveryFlagPasswordSpelling(t *testing.T) {
	t.Parallel()

	const secret = "sekret-value"
	for _, tc := range []struct {
		name string
		flag string
	}{
		{"userinfo password", "postgres://user:" + secret + "@localhost:5432/nova"},
		{"query password", "postgres://user@localhost:5432/nova?password=" + secret},
		{"query password whitespace-padded key", "postgres://user@localhost:5432/nova?password = " + secret},
		{"query password percent-encoded key", "postgres://user@localhost:5432/nova?%70assword=" + secret},
		{"keyword password", "host=localhost user=postgres password=" + secret + " dbname=nova"},
		{"keyword password whitespace-padded key", "host=localhost user=postgres password = " + secret + " dbname=nova"},
		{"keyword password quoted", "host=localhost password='" + secret + "' dbname=nova"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, func(string) string { return "" })
			require.Error(t, err, "%s: a --pg flag carrying a password must be refused", tc.name)
			assert.ErrorContains(t, err, "carries a password", "%s: the refusal names the problem", tc.name)
			assert.Empty(t, got, "%s: nothing is returned for a refused flag", tc.name)
			assert.NotContains(t, err.Error(), secret, "%s: the refusal must not echo the password", tc.name)
		})
	}
}

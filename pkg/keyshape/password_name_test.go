package keyshape

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSecretNameRecognizesPasswordShapes pins the gate credential leak at the
// predicate. A database password carries no KEY, TOKEN or SECRET, so a name
// like PGPASSWORD, NOVA_PG_PASSWORD or NOVA_REDIS_PASSWORD slipped past
// SecretName and reached a gate subprocess. The predicate stays the blunt
// substring test and folds case, in the safe direction: it over-scrubs.
func TestSecretNameRecognizesPasswordShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want bool
	}{
		{"PGPASSWORD", true},
		{"pgpassword", true},
		{"PgPassword", true},
		{"NOVA_PG_PASSWORD", true},
		{"nova_pg_password", true},
		{"NOVA_REDIS_PASSWORD", true},
		{"nova_redis_password", true},
		{"DB_PASSWD", true},
		{"db_passwd", true},
		{"MiXeD_PaSsWd", true},
		{"GH_TOKEN", true},
		{"DEEPSEEK_API_KEY", true},
		{"SOPS_AGE_SECRET", true},
		{"PATH", false},
		{"HOME", false},
		{"LANG", false},
		{"NOVA_SWARM_JOB", false},
		{"XDG_DATA_HOME", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, SecretName(tc.name), "SecretName(%q)", tc.name)
		})
	}
}

package redisauth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/redisauth"
)

// TestAuthRefusesAUserWhosePasswordVariableIsEmpty: a named ACL user whose
// password variable is unset is refused with the user, the variable and the
// nova-secrets remedy, and is never dialed as the default user (no user and
// no password come back). The variable name is one nothing sets, so the test
// reads no real environment.
func TestAuthRefusesAUserWhosePasswordVariableIsEmpty(t *testing.T) {
	t.Parallel()

	const unset = "NOVA_TEST_REDISAUTH_UNSET_7F3A"
	cases := []struct {
		name, user, passwordEnv string
		wantNamed               string
	}{
		{"flag user", "bench", unset, "--user bench"},
		{"flag user with a dash", "ci-leg", unset, "--user ci-leg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			user, password, err := redisauth.Auth(tc.user, tc.passwordEnv)
			assert.ErrorContains(t, err, tc.wantNamed+" but "+unset+" is empty")
			assert.ErrorContains(t, err, "nova-secrets exec --only "+unset)
			assert.Equal(t, "", user)
			assert.Equal(t, "", password)
		})
	}
}

// TestNoUserHintNamesThePair: the hint names the unset user variable, the
// password variable, the pair to set, and says the password comes from
// nova-secrets and never a flag.
func TestNoUserHintNamesThePair(t *testing.T) {
	t.Parallel()

	hint := redisauth.NoUserHint()
	assert.Contains(t, hint, redisauth.UserEnv+" is unset but "+redisauth.DefaultPasswordEnv+" is set")
	assert.Contains(t, hint, "set the pair "+redisauth.UserEnv+"=bench and "+redisauth.DefaultPasswordEnv)
	assert.Contains(t, hint, "nova-secrets exec --only "+redisauth.DefaultPasswordEnv)
	assert.Contains(t, hint, "never a flag")
	assert.NotContains(t, hint, "\n")
}

// TestEnvNames: the three variable names are the ones the fleet sets.
func TestEnvNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "NOVA_SPRINT_REDIS_USER", redisauth.UserEnv)
	assert.Equal(t, "NOVA_SPRINT_REDIS_PASSWORD_ENV", redisauth.PasswordEnvEnv)
	assert.Equal(t, "NOVA_REDIS_BENCH_PASSWORD", redisauth.DefaultPasswordEnv)
}

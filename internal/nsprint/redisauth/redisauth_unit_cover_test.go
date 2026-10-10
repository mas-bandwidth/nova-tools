package redisauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Auth tests do not call t.Parallel() because t.Setenv cannot be used in parallel tests.
// Each test clears the environment variables to prevent host environment leakage.

func TestRedisauthAuthCoverNoUserNoPassword(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(PasswordEnvEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	user, pass, err := Auth("", "")
	assert.Empty(t, user)
	assert.Empty(t, pass)
	assert.NoError(t, err)
}

func TestRedisauthAuthCoverNoUserWithPasswordEnv(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(PasswordEnvEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	const pw = "secret123"
	t.Setenv("NOVA_TEST_REDISAUTH_PW", pw)
	user, pass, err := Auth("", "NOVA_TEST_REDISAUTH_PW")
	assert.Empty(t, user)
	assert.Equal(t, pw, pass)
	assert.NoError(t, err)
}

func TestRedisauthAuthCoverExplicitUserWithPasswordEnv(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(PasswordEnvEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	const user = "bench"
	const pw = "pass456"
	t.Setenv("NOVA_TEST_REDISAUTH_PW", pw)
	gotUser, gotPass, err := Auth(user, "NOVA_TEST_REDISAUTH_PW")
	assert.Equal(t, user, gotUser)
	assert.Equal(t, pw, gotPass)
	assert.NoError(t, err)
}

func TestRedisauthAuthCoverExplicitUserPasswordEnvEmptyReadsPasswordEnvEnv(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	const user = "bench"
	const pw = "pass789"
	t.Setenv(PasswordEnvEnv, "NOVA_TEST_REDISAUTH_PW")
	t.Setenv("NOVA_TEST_REDISAUTH_PW", pw)
	gotUser, gotPass, err := Auth(user, "")
	assert.Equal(t, user, gotUser)
	assert.Equal(t, pw, gotPass)
	assert.NoError(t, err)
}

func TestRedisauthAuthCoverExplicitUserPasswordEnvEmptyPasswordEnvEnvEmptyReadsDefault(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(PasswordEnvEnv, "")
	const user = "bench"
	const pw = "defaultpass"
	t.Setenv(DefaultPasswordEnv, pw)
	gotUser, gotPass, err := Auth(user, "")
	assert.Equal(t, user, gotUser)
	assert.Equal(t, pw, gotPass)
	assert.NoError(t, err)
}

func TestRedisauthAuthCoverUserEnv(t *testing.T) {
	t.Setenv(PasswordEnvEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	const user = "bench"
	const pw = "envpass"
	t.Setenv(UserEnv, user)
	t.Setenv("NOVA_TEST_REDISAUTH_PW", pw)
	gotUser, gotPass, err := Auth("", "NOVA_TEST_REDISAUTH_PW")
	assert.Equal(t, user, gotUser)
	assert.Equal(t, pw, gotPass)
	assert.NoError(t, err)
}

func TestRedisauthAuthCoverExplicitUserEmptyPasswordVariableRefusal(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(PasswordEnvEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	const user = "bench"
	gotUser, gotPass, err := Auth(user, "")
	assert.Empty(t, gotUser)
	assert.Empty(t, gotPass)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--user "+user)
}

func TestRedisauthAuthCoverUserEnvEmptyPasswordVariableRefusal(t *testing.T) {
	t.Setenv(PasswordEnvEnv, "")
	t.Setenv(DefaultPasswordEnv, "")
	const user = "bench"
	t.Setenv(UserEnv, user)
	gotUser, gotPass, err := Auth("", "")
	assert.Empty(t, gotUser)
	assert.Empty(t, gotPass)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOVA_SPRINT_REDIS_USER="+user)
}

func TestRedisauthAuthCoverUserEnvEmptyPasswordVariableDefaultRefusal(t *testing.T) {
	t.Setenv(UserEnv, "")
	t.Setenv(PasswordEnvEnv, "")
	const user = "bench"
	t.Setenv(DefaultPasswordEnv, "")
	gotUser, gotPass, err := Auth(user, "")
	assert.Empty(t, gotUser)
	assert.Empty(t, gotPass)
	require.Error(t, err)
	assert.Contains(t, err.Error(), DefaultPasswordEnv)
}

func TestRedisauthNoUserHint(t *testing.T) {
	t.Parallel()
	got := NoUserHint()
	assert.Contains(t, got, UserEnv)
	assert.Contains(t, got, DefaultPasswordEnv)
	assert.Contains(t, got, "never a flag")
}

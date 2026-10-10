package redisauth

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedisauthAuthCover(t *testing.T) {
	// t.Setenv cannot be used in a parallel test
	t.Run("no user and no passwordEnv", func(t *testing.T) {
		os.Setenv(UserEnv, "")
		os.Setenv(PasswordEnvEnv, "")
		user, password, err := Auth("", "")
		assert.NoError(t, err)
		assert.Equal(t, "", user)
		assert.Equal(t, "", password)
	})

	t.Run("no user with passwordEnv naming a set variable", func(t *testing.T) {
		os.Setenv(UserEnv, "")
		os.Setenv(PasswordEnvEnv, "NOVA_TEST_PASSWORD")
		os.Setenv("NOVA_TEST_PASSWORD", "secret")
		user, password, err := Auth("", "NOVA_TEST_PASSWORD")
		assert.NoError(t, err)
		assert.Equal(t, "", user)
		assert.Equal(t, "secret", password)
	})

	t.Run("explicit user with passwordEnv set", func(t *testing.T) {
		os.Setenv(UserEnv, "")
		os.Setenv(PasswordEnvEnv, "NOVA_TEST_PASSWORD")
		os.Setenv("NOVA_TEST_PASSWORD", "secret")
		user, password, err := Auth("bench", "NOVA_TEST_PASSWORD")
		assert.NoError(t, err)
		assert.Equal(t, "bench", user)
		assert.Equal(t, "secret", password)
	})

	t.Run("explicit user with passwordEnv empty reads PasswordEnvEnv", func(t *testing.T) {
		os.Setenv(UserEnv, "")
		os.Setenv(PasswordEnvEnv, "NOVA_TEST_PASSWORD2")
		os.Setenv("NOVA_TEST_PASSWORD2", "secret2")
		user, password, err := Auth("bench", "")
		assert.NoError(t, err)
		assert.Equal(t, "bench", user)
		assert.Equal(t, "secret2", password)
	})

	t.Run("explicit user with passwordEnv empty reads DefaultPasswordEnv", func(t *testing.T) {
		os.Setenv(UserEnv, "")
		os.Setenv(PasswordEnvEnv, "")
		os.Setenv(DefaultPasswordEnv, "secret3")
		user, password, err := Auth("bench", "")
		assert.NoError(t, err)
		assert.Equal(t, "bench", user)
		assert.Equal(t, "secret3", password)
	})

	t.Run("user taken from UserEnv when argument is empty", func(t *testing.T) {
		os.Setenv(UserEnv, "bench4")
		os.Setenv(PasswordEnvEnv, "")
		os.Setenv(DefaultPasswordEnv, "secret4")
		user, password, err := Auth("", "")
		assert.NoError(t, err)
		assert.Equal(t, "bench4", user)
		assert.Equal(t, "secret4", password)
	})

	t.Run("explicit user whose password variable is empty returns error", func(t *testing.T) {
		os.Setenv(UserEnv, "")
		os.Setenv(PasswordEnvEnv, "NOVA_TEST_PASSWORD5")
		os.Setenv("NOVA_TEST_PASSWORD5", "")
		user, password, err := Auth("bench", "NOVA_TEST_PASSWORD5")
		assert.Error(t, err)
		assert.Equal(t, "", user)
		assert.Equal(t, "", password)
	})

	t.Run("user from UserEnv whose password variable is empty returns error", func(t *testing.T) {
		os.Setenv(UserEnv, "bench6")
		os.Setenv(PasswordEnvEnv, "NOVA_TEST_PASSWORD6")
		os.Setenv("NOVA_TEST_PASSWORD6", "")
		user, password, err := Auth("", "")
		assert.Error(t, err)
		assert.Equal(t, "", user)
		assert.Equal(t, "", password)
	})
}

func TestRedisauthAuthCoverNoUserHint(t *testing.T) {
	t.Parallel()

	hint := NoUserHint()
	assert.Contains(t, hint, UserEnv)
	assert.Contains(t, hint, DefaultPasswordEnv)
	assert.Contains(t, hint, "never a flag")
}

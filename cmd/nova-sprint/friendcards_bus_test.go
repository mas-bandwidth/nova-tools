package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
)

// The bus is dialed with its own login, never the sprint store's: with only the store's
// coordinator login in the environment, the bus connection asks for no user.
func TestTheBusIsNotDialedWithTheStoresLogin(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_SPRINT_REDIS_USER": "coordinator", "NOVA_SPRINT_REDIS_PASSWORD_ENV": "X"}
	assert.Empty(t, env[busUserEnv], "the store's login names no bus user")
	assert.Equal(t, "NOVA_BUS_REDIS_USER", busUserEnv)
	assert.NotEqual(t, "NOVA_SPRINT_REDIS_USER", busUserEnv)
}

// TestFriendSyncWakesOnTheBusWithTheBusLogin pins SPEC-SPRINT section 1:
// the bus connection does not borrow the sprint store's ACL credentials.
func TestFriendSyncWakesOnTheBusWithTheBusLogin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, busUser, busPasswordEnv string
	}{
		{name: "default bus user"},
		{name: "named bus user", busUser: "bus", busPasswordEnv: "Q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{
				"NOVA_SPRINT_REDIS_USER":         "coordinator",
				"NOVA_SPRINT_REDIS_PASSWORD_ENV": "P",
				"NOVA_BUS_REDIS":                 "bus.test:6379",
				"P":                              "test-password",
				"NOVA_BUS_REDIS_USER":            tc.busUser,
				"NOVA_BUS_REDIS_PASSWORD_ENV":    tc.busPasswordEnv,
				"Q":                              "test-password",
			}
			getenv := func(k string) string { return env[k] }
			o, err := redisconn.Resolve(busOptions(getenv), getenv)
			require.NoError(t, err)
			assert.Equal(t, "bus.test:6379", o.Addr)
			assert.Equal(t, tc.busUser, o.User, "bus login borrowed the sprint store's user")
			assert.Equal(t, tc.busPasswordEnv, o.PasswordEnv, "bus login borrowed the sprint store's password variable")
		})
	}
}

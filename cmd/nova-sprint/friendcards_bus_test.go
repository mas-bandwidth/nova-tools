package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

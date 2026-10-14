package bus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOpenRefusesNamingNovaBusRedisAndNeverDialsSprintWithoutALogin pins the
// address rule in SPEC-BUS.md, the config: a store named only by
// NOVA_SPRINT_REDIS is reached through the fleet row (fleet:bus), and that row
// is read with the fleet's login. With no login for the sprint store the read
// is the ACL's NOAUTH, so Address refuses before the dial and names
// NOVA_BUS_REDIS and --redis as the way to say where the bus is.
func TestOpenRefusesNamingNovaBusRedisAndNeverDialsSprintWithoutALogin(t *testing.T) {
	t.Parallel()
	env := map[string]string{SprintRedisEnv: "127.0.0.1:6380"}
	getenv := func(k string) string { return env[k] }
	dialled := false
	fleetBus := func(context.Context, string) (string, error) {
		dialled = true
		return "127.0.0.1:6381", nil
	}
	addr, refusal := Address(context.Background(), "", getenv, fleetBus)
	assert.False(t, dialled, "the sprint store is dialled without a login")
	assert.Empty(t, addr, "an address is returned without a login")
	assert.Contains(t, refusal, RedisEnv, "the refusal names the bus store's own variable")
	assert.Contains(t, refusal, "--redis", "the refusal names the flag that says where the bus is")
}

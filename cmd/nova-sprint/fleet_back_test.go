package main

import (
	"testing"
	"time"

	"github.com/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The binary adopts a member back from down only when the coordinator names
// adopt's flags and the binary knows the release it runs; then an adoption
// runs through release adopt for the one machine, once per episode.
func TestFleetBackAdopterIsTheReleaseAdoptPathForOneMachine(t *testing.T) {
	t.Parallel()
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == adoptFlagsEnv {
				return v
			}
			return ""
		}
	}
	assert.Nil(t, fleetBackAdopter(env(""), "v1.2.0"), "no flags: no adoption")
	assert.Nil(t, fleetBackAdopter(env("--no-certify"), ""), "no release stamped: no adoption")
	a := fleetBackAdopter(env("--no-certify --from /nowhere"), "v1.2.0")
	require.NotNil(t, a)
	assert.Equal(t, "v1.2.0", a.Target())
	assert.Equal(t, sprint.MachineAdoption{}, a.Adoption("m1", "e1"), "none started")
	require.True(t, a.Start("m1", "v1.2.0", "e1"))
	assert.False(t, a.Start("m1", "v1.2.0", "e1"), "never a second adoption of one episode")
	var got sprint.MachineAdoption
	require.Eventually(t, func() bool {
		got = a.Adoption("m1", "e1")
		return got.State != sprint.AdoptRunning
	}, 10*time.Second, 10*time.Millisecond)
	assert.Equal(t, sprint.AdoptFailed, got.State, "adopt refuses a release it cannot find, and says so")
	assert.NotEmpty(t, got.Err)
	assert.Equal(t, sprint.MachineAdoption{}, a.Adoption("m1", "e2"), "another episode has its own adoption")
}

package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
	missing := fleetBackAdopter(env(""), "v1.2.0")
	require.NotNil(t, missing, "missing flags still install a fail-closed adopter")
	assert.Empty(t, missing.Target())
	unreleased := fleetBackAdopter(env("--no-certify"), "dev")
	require.NotNil(t, unreleased, "an unstamped build still installs a fail-closed adopter")
	assert.Empty(t, unreleased.Target())
	a := fleetBackAdopter(env("--no-certify --from /nowhere"), "v1.2.0")
	require.NotNil(t, a)
	assert.Equal(t, "v1.2.0", a.Target())
	assert.Equal(t, sprint.Adoption{}, a.Adoption("m1", "e1"), "none started")
	assert.Equal(t, sprint.Adoption{}, a.Adoption("m1", "e2"), "another episode has its own adoption")
}

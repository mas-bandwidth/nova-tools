package friend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAntigravityCapabilityKeepsSurfaceAndReadinessSeparate(t *testing.T) {
	t.Parallel()
	for _, surface := range []string{"antigravity", "antigravity-cli", "unknown"} {
		t.Run(surface, func(t *testing.T) {
			t.Parallel()
			facts, err := AntigravitySetupCapability(surface)
			if surface == "unknown" {
				require.Error(t, err)
				assert.Nil(t, facts)
				assert.Contains(t, err.Error(), "select the desktop or CLI surface explicitly")
				return
			}
			require.NoError(t, err)
			states := map[string]string{}
			details := map[string]string{}
			for _, fact := range facts {
				states[fact.Name], details[fact.Name] = fact.State, fact.Detail
			}
			assert.Equal(t, surface, states["surface"])
			assert.Equal(t, "unestablished", states["readiness"])
			assert.Equal(t, "unestablished", states["portable-consumer-grant"])
			assert.Equal(t, "unknown", states["authentication"])
			assert.Equal(t, "unknown", states["allowance"])
			assert.Contains(t, details["allowance"], "no Gemini API fallback")
			if surface == "antigravity" {
				assert.Equal(t, "existing-adapter", states["transport"])
				assert.Contains(t, details["transport"], "desktop AgentAPI")
				assert.Contains(t, details["enrollment"], "existing desktop operation is unchanged")
			} else {
				assert.Equal(t, "documented", states["transport"])
				assert.Contains(t, details["transport"], "static permission settings exist")
				assert.Contains(t, details["transport"], "interactive control_request/control_response are rejected")
				assert.Contains(t, details["enrollment"], "per-target native SSH OAuth")
			}
		})
	}
}

func TestAntigravityCLIEnrollmentRefusalNamesSupportedRemedy(t *testing.T) {
	t.Parallel()
	err := AntigravityCLIFleetSetupRefusal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "per-target agy SSH OAuth URL/browser-code flow")
	assert.Contains(t, err.Error(), "included-only allowance")
	assert.Contains(t, err.Error(), "preserve the existing desktop adapter")
}

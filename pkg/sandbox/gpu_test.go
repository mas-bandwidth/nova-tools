package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGPUModeRejectsBlanketAccess(t *testing.T) {
	t.Parallel()

	_, err := ParseGPUMode("all")
	require.NotNil(t, err, "ParseGPUMode(\"all\") accepted a blanket GPU grant; only none|metal are explicit capabilities")
	m, err := ParseGPUMode("metal")
	require.Nil(t, err, "ParseGPUMode(metal) = %q, %v; want metal", m, err)
	require.Equal(t, GPUMetal, m, "ParseGPUMode(metal) = %q, %v; want metal", m, err)
}

// --gpu metal appends the measured Metal grants to the darwin profile and nothing wider;
// --gpu none (the default) appends none of them. SPEC-SANDBOX "Local GPU".
func TestMetalModeAppendsOnlyTheNamedGrants(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	policy := func(mode GPUMode) *Policy {
		return &Policy{
			Writes: []string{base}, Cwd: base, Tmp: base, Home: base,
			Command: "/bin/sh", Argv: []string{"/bin/sh"}, GPUMode: mode,
		}
	}
	metal, _, err := DarwinProfile(policy(GPUMetal))
	require.NoError(t, err, "the gpu=metal profile could not be generated")
	none, _, err := DarwinProfile(policy(GPUNone))
	require.NoError(t, err, "the gpu=none profile could not be generated")

	for _, grant := range []string{
		`(allow iokit-open (iokit-user-client-class "AGXDeviceUserClient" "IOSurfaceRootUserClient"))`,
		`(allow mach-lookup (xpc-service-name "com.apple.MTLCompilerService" "com.apple.coremedia.videoencoder" "com.apple.coremedia.videodecoder"))`,
	} {
		require.Contains(t, metal, grant, "gpu=metal is missing a measured grant; Metal or the video codec stays denied inside the wall")
		require.NotContains(t, none, grant, "gpu=none carries a Metal grant; the default wall must stay closed to the GPU")
	}
	for _, line := range strings.Split(metal, "\n") {
		trimmed := strings.TrimSpace(line)
		require.NotEqual(t, "(allow iokit-open)", trimmed, "gpu=metal granted every IOKit user client")
		require.NotEqual(t, "(allow mach-lookup)", trimmed, "gpu=metal granted every mach service")
		require.NotContains(t, trimmed, "global-name-prefix", "gpu=metal widened mach-lookup by prefix")
	}
	require.Equal(t, none+MetalGrants, metal, "gpu=metal differs from gpu=none by anything but MetalGrants")
}

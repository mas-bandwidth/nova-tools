package main

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
)

// A member started with --gpu metal hands each child's wall --gpu metal, before the --
// that starts the harness; a member with none (the default) hands the wall no --gpu, so
// the wall stays closed to the GPU (pkg/sandbox/gpu.go MetalGrants).
func TestTheWallGetsGPUMetalOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	cfg := nativeRunConfig{binary: "/opt/bin/opencode", model: "p/m", slotDir: "/s", benchHome: t.TempDir(), benchOS: "darwin", noSharedCaches: true}
	plain := nativeSandboxArgv([]string{"/opt/bin/opencode"}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	assert.NotContains(t, plain, "--gpu", "a wall with no GPU capability asked for was handed --gpu")

	cfg.gpu = sandbox.GPUMetal
	metal := nativeSandboxArgv([]string{"/opt/bin/opencode"}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	i := slices.Index(metal, "--gpu")
	require.GreaterOrEqual(t, i, 0, "a --gpu metal run handed the wall no --gpu: Metal stays denied inside it")
	require.Less(t, i+1, len(metal))
	assert.Equal(t, "metal", metal[i+1])
	dash := slices.Index(metal, "--")
	require.GreaterOrEqual(t, dash, 0, "the wall argv has no -- before the harness")
	assert.Less(t, i, dash, "--gpu came after the --, so the harness got it instead of the wall")
}

// The member forwards its own --gpu to each child's native argv, and only metal.
func TestMemberLaunchArgsCarryGPU(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode sandbox.GPUMode
		want bool
	}{{"", false}, {sandbox.GPUNone, false}, {sandbox.GPUMetal, true}} {
		args := gpuArgs(tc.mode)
		assert.Equal(t, tc.want, slices.Equal(args, []string{"--gpu", "metal"}), "mode %q: native args %v", tc.mode, args)
		if !tc.want {
			assert.Empty(t, args, "mode %q handed native a --gpu", tc.mode)
		}
	}
}

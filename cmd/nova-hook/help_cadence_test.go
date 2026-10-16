package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHookHelpNamesServerCadence pins the client and heartbeat challenge clocks (docs/SPEC-HOOK.md, The clocks).
func TestHookHelpNamesServerCadence(t *testing.T) {
	t.Parallel()

	result := newRig(t).cli().Run("hook", "--help")
	require.Zero(t, result.Code, result.Stdout+result.Stderr)
	assert.Contains(t, result.Stdout, "A standalone loop asks at start and every "+hook.ChallengeEvery.String())
	assert.Contains(t, result.Stdout, "The heartbeat server checks every hooked name whose")
	assert.Contains(t, result.Stdout, "role carries the bus push every "+hook.BeatEvery.String())
	assert.Contains(t, result.Stdout, "Proof renewal starts "+(hook.ProofTTL-hook.AnswerBound-2*hook.BeatEvery).String()+" after the last proof")
	serve := newRig(t).cli().Run("serve", "--help")
	require.Zero(t, serve.Code, serve.Stdout+serve.Stderr)
	assert.Contains(t, serve.Stdout, "default "+(hook.ProofTTL-hook.AnswerBound-2*hook.BeatEvery).String()+" after proof")
}

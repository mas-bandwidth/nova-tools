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
	assert.Contains(t, result.Stdout, "client prints a bootstrap challenge at start. A standalone hook loop repeats its own challenges every "+hook.ChallengeEvery.String())
	assert.Contains(t, result.Stdout, "heartbeat server challenges them every --every (default "+hook.BeatEvery.String()+")")
	assert.Contains(t, result.Stdout, "marks a name down after --down-after (default "+hook.DownAfter.String()+") silent")
}

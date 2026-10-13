package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFuseStatusJSON pins card fuse-status-json: status --json emits one
// structured document (lockdown state, quarantines with since/reason) at
// exit 0, and plain status output is unchanged without --json.
func TestFuseStatusJSON(t *testing.T) {
	t.Parallel()

	box := boxIn(t)
	exit, _, stderr := runFuse(t, "quarantine", "--box", box, "discord", "many the same way")
	require.Equal(t, 0, exit, "setup quarantine: %s", stderr)
	exit, _, stderr = runFuse(t, "lockdown", "--box", box, "suspected compromise")
	require.Equal(t, 0, exit, "setup lockdown: %s", stderr)

	exit, stdout, stderr := runFuse(t, "status", "--box", box, "--json")
	require.Equal(t, 0, exit, "status --json exit: stderr %s", stderr)
	assert.Empty(t, stderr)

	var doc struct {
		Lockdown struct {
			State  string `json:"state"`
			Since  string `json:"since"`
			Reason string `json:"reason"`
		} `json:"lockdown"`
		Quarantines []struct {
			Surface string `json:"surface"`
			Since   string `json:"since"`
			Reason  string `json:"reason"`
		} `json:"quarantines"`
		QuarantineCount int `json:"quarantine_count"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc), "stdout must be one JSON document: %q", stdout)
	assert.Equal(t, "blown", doc.Lockdown.State)
	assert.Equal(t, "suspected compromise", doc.Lockdown.Reason)
	assert.NotEmpty(t, doc.Lockdown.Since)
	require.Len(t, doc.Quarantines, 1)
	assert.Equal(t, "discord", doc.Quarantines[0].Surface)
	assert.Equal(t, "many the same way", doc.Quarantines[0].Reason)
	assert.NotEmpty(t, doc.Quarantines[0].Since)
	assert.Equal(t, 1, doc.QuarantineCount)

	// Plain output unchanged.
	exit, stdout, _ = runFuse(t, "status", "--box", box)
	require.Equal(t, 0, exit)
	assert.Contains(t, stdout, "STATUS OK lockdown=blown")

	// Clear box: lockdown clear, empty (non-null) quarantines.
	clearBox := boxIn(t)
	exit, stdout, _ = runFuse(t, "status", "--box", clearBox, "--json")
	require.Equal(t, 0, exit)
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	assert.Equal(t, "clear", doc.Lockdown.State)
	assert.NotNil(t, doc.Quarantines)
	assert.Empty(t, doc.Quarantines)
	assert.Equal(t, 0, doc.QuarantineCount)

	// Absent box under --json: refused record on stdout, exit 2.
	exit, stdout, _ = runFuse(t, "status", "--box", absentBoxIn(t), "--json")
	require.Equal(t, 2, exit)
	var refusal struct {
		Status string `json:"status"`
		Remedy string `json:"remedy"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &refusal), "refused stdout: %q", stdout)
	assert.Equal(t, "refused", refusal.Status)
	assert.Contains(t, refusal.Remedy, "run:")
}

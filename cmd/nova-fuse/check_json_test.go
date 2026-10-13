package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFuseCheckAndReceiptsJSON pins card fuse-check-and-receipts-json:
// check --json and quarantine --json emit structured records on stdout
// while fail-closed exit codes (0 clear, 1 blown, 2 refused) are kept.
func TestFuseCheckAndReceiptsJSON(t *testing.T) {
	t.Parallel()

	box := boxIn(t)

	// quarantine --json receipt, exit 0, and the write lands.
	exit, stdout, stderr := runFuse(t, "quarantine", "--box", box, "--json", "discord", "many the same way")
	require.Equal(t, 0, exit, "quarantine --json: stderr %s", stderr)
	var receipt struct {
		Verb    string `json:"verb"`
		Status  string `json:"status"`
		Surface string `json:"surface"`
		Reason  string `json:"reason"`
		DryRun  bool   `json:"dry_run"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &receipt), "quarantine stdout: %q", stdout)
	assert.Equal(t, "quarantine", receipt.Verb)
	assert.Equal(t, "ok", receipt.Status)
	assert.Equal(t, "discord", receipt.Surface)
	assert.Equal(t, "many the same way", receipt.Reason)
	assert.False(t, receipt.DryRun)

	// check --json on the quarantined surface: blown, exit 1.
	exit, stdout, _ = runFuse(t, "check", "--box", box, "--json", "discord")
	require.Equal(t, 1, exit)
	var decision struct {
		Verb       string `json:"verb"`
		Status     string `json:"status"`
		Quarantine *struct {
			Surface string `json:"surface"`
			Reason  string `json:"reason"`
		} `json:"quarantine"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decision), "check stdout: %q", stdout)
	assert.Equal(t, "blown", decision.Status)
	require.NotNil(t, decision.Quarantine)
	assert.Equal(t, "discord", decision.Quarantine.Surface)

	// check --json on a clear surface: exit 0.
	exit, stdout, _ = runFuse(t, "check", "--box", box, "--json", "other")
	require.Equal(t, 0, exit)
	require.NoError(t, json.Unmarshal([]byte(stdout), &decision))
	assert.Equal(t, "clear", decision.Status)

	// check --json with no box: refused, exit 2, still JSON.
	exit, stdout, _ = runFuse(t, "check", "--box", absentBoxIn(t), "--json", "discord")
	require.Equal(t, 2, exit)
	require.NoError(t, json.Unmarshal([]byte(stdout), &decision), "refused stdout: %q", stdout)
	assert.Equal(t, "refused", decision.Status)

	// Plain check output unchanged.
	exit, stdout, _ = runFuse(t, "check", "--box", box, "other")
	require.Equal(t, 0, exit)
	assert.Contains(t, stdout, "FUSE OK")

	// Lockdown (the hard gate) under --json: blown, exit 1.
	lockBox := boxIn(t)
	exit, _, stderr = runFuse(t, "lockdown", "--box", lockBox, "suspected compromise")
	require.Equal(t, 0, exit, "setup lockdown: %s", stderr)
	exit, stdout, _ = runFuse(t, "check", "--box", lockBox, "--json", "discord")
	require.Equal(t, 1, exit)
	require.NoError(t, json.Unmarshal([]byte(stdout), &decision))
	assert.Equal(t, "blown", decision.Status)
}

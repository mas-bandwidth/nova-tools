package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyHelpSaysItWritesAReceiptAndFlagRefusalsNameTheirDoor pins the audit
// findings on this tool: verify -h claims "writes nothing" yet writes a receipt,
// verify rejects --max 0 though 0 lists all, flag refusals omit the REFUSED word
// and the remedy, the step usage line omits [--dry-run], and the install synopsis
// names mirror-refresh though it is refused here.
func TestVerifyHelpSaysItWritesAReceiptAndFlagRefusalsNameTheirDoor(t *testing.T) {
	t.Parallel()

	verifyHelp := swarmHelp(t, "verify", "-h")
	assert.Contains(t, verifyHelp, "effect: local write", "verify -h does not say it writes:\n%s", verifyHelp)
	assert.Contains(t, verifyHelp, "receipt", "verify -h does not name the receipt:\n%s", verifyHelp)
	assert.NotContains(t, verifyHelp, "writes nothing", "verify -h still claims it writes nothing:\n%s", verifyHelp)

	result := filepath.Join(t.TempDir(), "RESULT.md")
	exit, _, stderr := runSwarm(t, "verify", "--result", result, "--contract", "c", "--label", "l", "--max", "0")
	assert.Equal(t, 2, exit, "verify with an unreadable RESULT.md exits %d, want 2", exit)
	assert.NotContains(t, stderr, "--max", "verify --max 0 was refused as invalid:\n%s", stderr)

	exit, _, stderr = runSwarm(t, "step")
	assert.Equal(t, 2, exit, "step with no --card exits %d, want 2", exit)
	assert.Contains(t, stderr, "REFUSED", "step with no --card omits REFUSED:\n%s", stderr)
	assert.Contains(t, stderr, "run: nova-swarm help step", "step with no --card omits the remedy:\n%s", stderr)

	help := swarmHelp(t, "help")
	found := false
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "nova-swarm step ") {
			assert.Contains(t, line, "[--dry-run]", "the step usage omits [--dry-run]:\n%s", line)
			found = true
		}
	}
	require.True(t, found, "the banner has no step usage line:\n%s", help)

	installHelp := swarmHelp(t, "install", "-h")
	assert.NotContains(t, installHelp, "mirror-refresh", "install -h still names mirror-refresh:\n%s", installHelp)
	assert.NotContains(t, help, "mirror-refresh", "the banner's install synopsis still names mirror-refresh:\n%s", help)
}

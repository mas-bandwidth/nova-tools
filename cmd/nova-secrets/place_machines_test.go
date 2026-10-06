package main

// TestPlaceRefusesGateMachinesFlag verifies that place rejects the gate
// --machines flag (used by gate to read the seat-voching registry) and
// requires its own --fleet flag for the machine table. This ensures the
// two verbs cannot be confused when given the wrong registry file.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlaceRefusesGateMachinesFlag(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	// Use --machines flag (gate's flag) instead of --fleet
	stdout, stderr, code := runNovaSecrets(f.bin,
		"place",
		"--store", f.store, "--as", "rowan", "--key", f.key, "--sops", f.sops,
		"--machine", "mini", "--secret", "DEEPSEEK_API_KEY",
		"--machines", f.machines, // This should be refused
		"--receipts", f.receipts, "--ssh", f.ssh,
	)
	require.Equal(t, 2, code, "place exit=%d want 2 for unknown flag", code)
	require.Contains(t, stderr, "--machines", "refusal %q must name the gate flag", stderr)
	require.Contains(t, stderr, "--fleet", "refusal %q must name the expected fleet flag", stderr)
	require.NotContains(t, stdout, "OK", "place should not succeed with gate flag", stdout)
}

func TestPlaceUsesOwnFlagName(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	// Use --fleet flag (place's own flag)
	stdout, stderr, code := runNovaSecrets(f.bin,
		"place",
		"--store", f.store, "--as", "rowan", "--key", f.key, "--sops", f.sops,
		"--machine", "mini", "--secret", "DEEPSEEK_API_KEY",
		"--fleet", f.machines, // This is place's flag
		"--receipts", f.receipts, "--ssh", f.ssh,
	)
	require.Equal(t, 0, code, "place exit=%d stderr=%q", code, stderr)
	require.Contains(t, stdout, "OK", "place should succeed with fleet flag", stdout)
}

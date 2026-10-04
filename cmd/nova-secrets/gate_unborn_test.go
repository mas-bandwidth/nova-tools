package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGateRefusesAStoreWithNoCommit: a store with no commit yet has no base and no head
// to compare, and the gate refuses it on stderr at exit 2, never an approval.
func TestGateRefusesAStoreWithNoCommit(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput()
	require.NoError(t, err, "git init: %v, out: %s", err, out)

	stdout, stderr, code := runNovaSecrets(bin, "gate", "--store", dir, "--base", "HEAD", "--head", "HEAD")

	require.Equal(t, 2, code, "unborn store: expected exit code 2 (REFUSE), got %d (stdout=%q, stderr=%q)", code, stdout, stderr)

	require.Empty(t, stdout, "unborn store: gate wrote to stdout: %q", stdout)

	require.Contains(t, stderr, "GATE REFUSE", "unborn store: gate stderr does not contain GATE REFUSE: %q", stderr)
}

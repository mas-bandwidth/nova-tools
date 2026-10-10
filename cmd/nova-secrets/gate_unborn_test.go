package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
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

	require.Contains(t, stderr, "SECRETS GATE REFUSED", "unborn store: gate stderr does not contain SECRETS GATE REFUSED: %q", stderr)
}

// TestGateVerdictExitsOneAtTheProcessBoundary: a rule verdict is the gate that ran and
// judged the diff, so the binary itself prints GATE FAILED and exits 1 with nothing on
// stdout (skeleton contract 1.2, STANDARD §2). A workflow step reads the exit apart from
// the could-not-run REFUSED at exit 2, where both were exit 2 before.
func TestGateVerdictExitsOneAtTheProcessBoundary(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v %s", args, err, out)
		return string(out)
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	base := strings.TrimSpace(git("rev-parse", "HEAD"))

	// A change outside .sops.yaml, README.md and the seat files is check 3.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("a change outside the gate\n"), 0o600))
	git("add", "-A")
	git("commit", "-q", "-m", "change")

	stdout, stderr, code := runNovaSecrets(bin, "gate", "--store", dir, "--base", base, "--head", "HEAD")
	require.Equal(t, 1, code, "rule verdict: expected exit 1 (FAILED), got %d (stdout=%q, stderr=%q)", code, stdout, stderr)
	require.Empty(t, stdout, "rule verdict: gate wrote to stdout: %q", stdout)
	require.True(t, strings.HasPrefix(stderr, "GATE FAILED"), "rule verdict: want a GATE FAILED line, got %q", stderr)
}

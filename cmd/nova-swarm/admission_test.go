package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const admissionMismatch = "../../internal/swarm/testdata/admission-mismatch.md"

// A card that fails only result-first drifts under a bare lint, and the note
// says that token does not bind admission. The same text under --child-rules
// still names only result-first, because the fixture carries the general rules.
func TestLintNamesAShapeDriftThatDoesNotBindAdmission(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"lint", "--card", admissionMismatch},
		{"lint", "--card", admissionMismatch, "--child-rules"},
	} {
		exit, stdout, stderr := runSwarm(t, args...)
		require.Equal(t, 1, exit, "%v: exit %d\n%s%s", args, exit, stdout, stderr)
		require.Equal(t, 1, strings.Count(stdout, "LINT DRIFT"), stdout)
		require.Contains(t, stdout, "LINT DRIFT card=admission-mismatch.md result-first:")
		require.Contains(t, stdout, "LINT NOTE card=admission-mismatch.md admission=not-bound tokens=result-first remedy=")
		require.Contains(t, stdout, swarm.AdmissionContract)
		require.Empty(t, stderr)
	}
}

// Repairing line 1 and dropping the RULES paragraph makes the bare lint clean.
// The child-rule drifts that remain bind admission, so the note stays absent,
// and add still has those drifts to refuse.
func TestLintOmitsTheAdmissionNoteWhenEveryDriftBinds(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(admissionMismatch)
	require.NoError(t, err)
	text := "RESULT: done\n" + strings.SplitN(string(raw), "\n", 2)[1]
	i := strings.Index(text, "\nRULES.\n")
	require.Greater(t, i, 0)
	text = text[:i+1]
	path := filepath.Join(t.TempDir(), "shape.md")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))

	exit, stdout, stderr := runSwarm(t, "lint", "--card", path)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	require.NotContains(t, stdout, "admission=not-bound")
	lines := strings.Count(strings.TrimSuffix(stdout, "\n"), "\n") + 1
	require.Equal(t, 1, lines, "a clean lint is one line:\n%s", stdout)

	exit, stdout, stderr = runSwarm(t, "lint", "--card", path, "--child-rules")
	require.Equal(t, 1, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	require.Contains(t, stdout, "LINT DRIFT card=shape.md rule-worktree:")
	require.NotContains(t, stdout, "admission=not-bound")
	require.Empty(t, stderr)
}

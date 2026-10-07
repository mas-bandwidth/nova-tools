package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ci_never_force_test.go is the red-test contract of the never-force checker in
// docs/SPEC-CI.md. Each fixture is a real file written into a throwaway tree
// and handed to CheckNeverForce, so the checker is exercised on a tree given
// on the command line and never by reaching into the repository.

// forceFixtureTree writes one fixture under <tmp>/internal/fixture/fixture_test.go
// and returns the tree root.
func forceFixtureTree(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "never-force", name))
	require.NoError(t, err)
	dir := filepath.Join(root, "internal", "fixture")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644))
	return root
}

// forceEmptyTree is a root with no source files at all.
func forceEmptyTree(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// forceLineAt reads the line the finding named and returns it trimmed.
func forceLineAt(t *testing.T, root, rel string, line int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	require.GreaterOrEqual(t, line, 1, "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	require.False(t, line > len(lines), "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	return strings.TrimSpace(lines[line-1])
}

// TestNeverForceRefusesPushForce: push --force is refused.
func TestNeverForceRefusesPushForce(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "push_force.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "push --force is one refusal, got %d: %+v", res.Refused(), res.Findings)
	require.Len(t, res.Findings, 1, "push --force is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "force", f.Kind, "kind = %q, want force", f.Kind)
	assert.Equal(t, NeverForceRemedyPush, f.Remedy, "remedy = %q, want %q", f.Remedy, NeverForceRemedyPush)
	got := forceLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "push --force", "finding names %s:%d = %q, want the push --force line", f.File, f.Line, got)
}

// TestNeverForceRefusesPushMinusF: push -f is refused.
func TestNeverForceRefusesPushMinusF(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "push_minus_f.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "push -f is one refusal, got %d: %+v", res.Refused(), res.Findings)
	require.Len(t, res.Findings, 1, "push -f is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "force", f.Kind)
	got := forceLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "push -f", "finding names %s:%d = %q", f.File, f.Line, got)
}

// TestNeverForceRefusesForceWithLease: --force-with-lease is refused.
func TestNeverForceRefusesForceWithLease(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "force_with_lease.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "--force-with-lease is one refusal, got %d: %+v", res.Refused(), res.Findings)
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	got := forceLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "--force-with-lease")
}

// TestNeverForceRefusesPushOriginPlus: push origin + is refused.
func TestNeverForceRefusesPushOriginPlus(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "push_origin_plus.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "push origin + is one refusal, got %d: %+v", res.Refused(), res.Findings)
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	got := forceLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "push origin +")
}

// TestNeverForceRefusesResetHardOrigin: reset --hard origin/ is refused.
func TestNeverForceRefusesResetHardOrigin(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "reset_hard_origin.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "reset --hard origin/ is one refusal, got %d: %+v", res.Refused(), res.Findings)
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	got := forceLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "reset --hard origin/")
}

// TestNeverForceAllowsNormalPush: a normal push is allowed.
func TestNeverForceAllowsNormalPush(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "normal_push.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 0, res.Refused(), "a normal push is allowed, got %d refusals: %+v", res.Refused(), res.Findings)
}

// TestNeverForceAllowlistGrowsRefused: adding an allowlist entry that names no offender is refused.
func TestNeverForceAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()

	root := forceEmptyTree(t)
	allow := filepath.Join(t.TempDir(), "never-force-allowlist.txt")

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err := CheckNeverForce(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())

	require.NoError(t, os.WriteFile(allow, []byte("internal/x/x_test.go:1 force 2026-10-07 parked here\n"), 0o644))
	res, err = CheckNeverForce(root, allow)
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	require.Len(t, res.Stale, 1)
	assert.Equal(t, NeverForceRemedyAllow, res.Stale[0].Remedy, "allowlist remedy = %q, want %q", res.Stale[0].Remedy, NeverForceRemedyAllow)
}

// TestNeverForceOutputMatchesTheSpec: pins the one-line grammar.
func TestNeverForceOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()

	root := forceFixtureTree(t, "push_force.go.txt")
	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	assert.Equal(t, "CI-NEVER-FORCE OK files=1 allowlisted=0 refused=0", res.OKLine(), "clean line = %q, want %q", res.OKLine(), "CI-NEVER-FORCE OK files=1 allowlisted=0 refused=0")
	assert.Equal(t, "CI-NEVER-FORCE FAIL files=1 allowlisted=0 refused=1", res.FailLine(), "fail line = %q, want %q", res.FailLine(), "CI-NEVER-FORCE FAIL files=1 allowlisted=0 refused=1")
	assert.Equal(t, 2, res.ExitCode(), "exit = %d, want 2", res.ExitCode())
	require.Len(t, res.Findings, 1)
	line := res.Findings[0].Render()
	assert.Contains(t, line, "CI-NEVER-FORCE file=internal/fixture/fixture_test.go line=")
}

// TestNeverForceVerbLineMatchesTheSpec: pins the help line.
func TestNeverForceVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	assert.Contains(t, spec, NeverForceVerbLine, "the never-force verb line is not in docs/SPEC-CI.md:\n%s", NeverForceVerbLine)
}

// TestNoForcePushOrHardResetOfASharedRef is the class test itself.
func TestNoForcePushOrHardResetOfASharedRef(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := filepath.Join(root, "internal", "ci", "never_force_allowlist.txt")
	res, err := CheckNeverForce(root, allow)
	require.NoError(t, err)
	t.Log(res.OKLine())
	for _, f := range res.Findings {
		t.Error(f.Render())
	}
}

package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// never_force_class_test.go is the red-test contract of the never-force checker, and then the
// class test itself over this repository. The checker reads every .go file, shell script,
// Makefile, and .github/workflows/*.yml file and refuses patterns that rewrite shared refs:
// `push --force`, `push -f`, `--force-with-lease` (except when used safely with refs/heads/),
// `push origin +`, and `reset --hard origin/`. The allowlist holds legitimate uses found
// in fixtures (test code) and scripts that must use these patterns for specific reasons.

const neverForceAllowlistPath = "internal/ci/never_force_allowlist.txt"

// TestNeverForceVerbLineMatchesTheSpec asserts the verb line appears in docs/SPEC-CI.md.
func TestNeverForceVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), NeverForceVerbLine, "the never-force verb line is not in docs/SPEC-CI.md:\n%s", NeverForceVerbLine)
}

// TestNeverForceRefusesAForcePushToDev is the checker's witness: a shell script
// that force-pushes dev, written into a tree handed to the checker, is refused
// with its file, line and kind; the checker reads the tree it is given and never
// the repository.
func TestNeverForceRefusesAForcePushToDev(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deploy.sh"),
		[]byte("#!/bin/sh\ngit push --force origin dev\n"), 0o644))

	res, err := CheckNeverForce(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, res.Refused(), "the force push to dev is one refusal, got %d: %+v", res.Refused(), res.Findings)
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	assert.Equal(t, "scripts/deploy.sh", f.File)
	assert.Equal(t, 2, f.Line)
	assert.Equal(t, "push-force", f.Kind)
	assert.Contains(t, f.Render(), "CI-NEVER-FORCE file=scripts/deploy.sh line=2 kind=push-force")
	assert.Equal(t, 2, res.ExitCode())
}

// TestNoForcePushOrHardResetOfASharedRef is the class test itself. Every .go file,
// shell script, Makefile, and .github/workflows/*.yml under the repository must not
// use force-push patterns against shared refs (dev, main, origin/<anything> without
// a local branch qualifier). The allowlist holds legitimate test fixtures and scripts.
func TestNoForcePushOrHardResetOfASharedRef(t *testing.T) {
	t.Parallel()

	res, err := CheckNeverForce(repoRoot(t), filepath.Join(repoRoot(t), neverForceAllowlistPath))
	require.NoError(t, err)

	for _, f := range res.Findings {
		t.Errorf("%s", f.Render())
	}

	list := loadAllowlist(t, filepath.Join(repoRoot(t), neverForceAllowlistPath), FileLineListOptions)
	checked := allowlist.Check(t, list, res.Measured)
	for _, row := range checked.Stale {
		t.Errorf("%s:%d lists %s, but nothing there uses force patterns any more; %s",
			neverForceAllowlistPath, row.Line, row.Key, NeverForceRemedyAllow)
	}

	require.NotZero(t, res.Files, "the walk read no files; the repository root is wrong")
	require.Zero(t, res.Refused(), "%s", res.FailLine())
}

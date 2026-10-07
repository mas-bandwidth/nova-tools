package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTreeGateRunsFunctionalChecks verifies that the gate includes functional tests
func TestTreeGateRunsFunctionalChecks(t *testing.T) {
	t.Parallel()

	// Create a temporary clone of the repository
	dir := t.TempDir()

	// Setup go.mod
	goMod := `module github.com/mas-bandwidth/nova-tools

go 1.22
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644))

	// Create internal/ci with functional tests
	internalCiDir := filepath.Join(dir, "internal", "ci")
	require.NoError(t, os.MkdirAll(internalCiDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(internalCiDir, "onboarding_functional_test.go"), []byte("package ci"), 0o644))

	// Create internal/docs
	internalDocsDir := filepath.Join(dir, "internal", "docs")
	require.NoError(t, os.MkdirAll(internalDocsDir, 0o755))

	// hasFunctionalTests should return true
	require.True(t, hasFunctionalTests(dir), "should detect functional tests")
}

// TestTreeGateBuildsAndVets verifies that the gate runs go build and go vet
func TestTreeGateBuildsAndVets(t *testing.T) {
	t.Parallel()

	// Verify gateRuns includes build and vet as first two runs
	runs := gateRuns(false, nil, false)

	require.GreaterOrEqual(t, len(runs), 2, "gateRuns should include at least build and vet")
	require.Equal(t, []string{"go", "build", "./..."}, runs[0], "first run should be go build ./...")
	require.Equal(t, []string{"go", "vet", "./..."}, runs[1], "second run should be go vet ./...")
}

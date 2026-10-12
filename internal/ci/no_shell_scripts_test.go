package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoShellScripts tracks the rule that no *.sh or *.bash file is allowed in the
// repository. It walks git ls-files and fails on any match, with no exception list.
func TestNoShellScripts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	raw, err := cmd.Output()
	require.NoError(t, err, "git ls-files in %s", root)
	tracked := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 })
	require.NotEmpty(t, tracked, "an empty tree is not a pass")

	var shellFiles []string
	for _, p := range tracked {
		lower := strings.ToLower(p)
		if strings.HasSuffix(lower, ".sh") || strings.HasSuffix(lower, ".bash") {
			shellFiles = append(shellFiles, p)
		}
	}

	if len(shellFiles) > 0 {
		assert.Failf(t, "no shell scripts allowed", "found %d tracked *.sh or *.bash file(s):\n  %s", len(shellFiles), strings.Join(shellFiles, "\n  "))
	}
}

// TestNoShellScriptsFailsOnAddedScript verifies the rule by showing the test fails
// when a *.sh file is added to a temporary tree.
func TestNoShellScriptsFailsOnAddedScript(t *testing.T) {
	t.Parallel()

	// Create a temporary git repository
	tmp := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())

	// Configure git for the test
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())

	// Create a go.mod to make it a valid go module
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/test\ngo 1.22\n"), 0644))

	// Add initial files
	cmd = exec.Command("git", "add", "go.mod")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())

	// Now create a shell script
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "run.sh"), []byte("#!/bin/sh\necho hello\n"), 0755))

	cmd = exec.Command("git", "add", "run.sh")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())

	cmd = exec.Command("git", "commit", "-m", "add shell script")
	cmd.Dir = tmp
	require.NoError(t, cmd.Run())

	// Run git ls-files and check for shell files
	cmd = exec.Command("git", "ls-files", "-z")
	cmd.Dir = tmp
	raw, err := cmd.Output()
	require.NoError(t, err)
	tracked := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 })

	var shellFiles []string
	for _, p := range tracked {
		lower := strings.ToLower(p)
		if strings.HasSuffix(lower, ".sh") || strings.HasSuffix(lower, ".bash") {
			shellFiles = append(shellFiles, p)
		}
	}

	assert.Equal(t, 1, len(shellFiles), "should find exactly one shell script")
	assert.Equal(t, "run.sh", shellFiles[0])
}

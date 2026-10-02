//go:build functional

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Onboarding point 5: the binary's standalone first run needs no fixture or remote.
func TestStandaloneFirstRunWorksWithOnlyACommittedRoster(t *testing.T) {
	t.Parallel()
	hermetic(t)
	root := filepath.Join(t.TempDir(), "bus")
	require.NoError(t, os.Mkdir(root, 0755))
	gitIn(t, root, "init", "-b", "main")
	gitIn(t, root, "config", "user.name", "Example")
	gitIn(t, root, "config", "user.email", "example@example.com")
	writeFile(t, root, "participants.json", `{"participants":[{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"}]}`)
	gitIn(t, root, "add", "participants.json")
	gitIn(t, root, "commit", "-m", "roster")
	for _, lane := range []string{"from-ada", "from-bo"} {
		_, err := os.Stat(filepath.Join(root, lane))
		require.True(t, os.IsNotExist(err), "setup must not invent a lane directory")
	}
	for _, args := range [][]string{
		{"names", "--bus", root},
		{"check", "--bus", root, "--full"},
		{"inbox", "--bus", root, "--as", "Ada", "--receipt-max-words", "40", "--full", "--open"},
		{"inbox", "--bus", root, "--as", "Bo", "--receipt-max-words", "40", "--full", "--open"},
		{"draft", "--bus", root, "--as", "Ada", "--to", "Bo", "--subject", "gate"},
	} {
		got := runAt(t, "", args...)
		require.Equal(t, 0, got.code, "command %v: stdout=%s stderr=%s", args, got.stdout, got.stderr)
		if args[0] == "draft" {
			assert.Contains(t, got.stdout, "To: Bo")
		}
	}
	assert.Empty(t, gitIn(t, root, "status", "--porcelain"), "first-run inspection and stdout draft leave the bus clean")
}

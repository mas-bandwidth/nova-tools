package docs

// TestAgentsmapCover* cover RunAgentsMap (agentsmap.go:390), the one function
// the unit tier's per-function table names at 0.0%. RunAgentsMap is the entry
// internal/docs exposes to tools/agentsmap: it refuses arguments, resolves the
// repository root, renders every page from the catalog and the live tree, and
// writes them.
//
// Both the refusal and the main path are reachable without a subprocess or a
// store. The main path takes its root from the working directory (RepoRoot), so
// the test runs it from the checkout itself: it regenerates the committed pages
// byte for byte, and the assertions hold the written files to what Render
// builds, so a real drift in the tree would show here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentsmapCoverRunAgentsMapRefusesArgs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"one argument", []string{"extra"}},
		{"a verb the tool does not have", []string{"check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RunAgentsMap(tc.args)
			require.Error(t, err, "an argument the tool takes nothing for is a refusal")
			assert.Contains(t, err.Error(), "usage: make map", "the refusal names the door that regenerates the map")
		})
	}
}

func TestAgentsmapCoverRunAgentsMapWritesTheMap(t *testing.T) {
	t.Parallel()

	require.NoError(t, RunAgentsMap(nil), "the checkout's own root regenerates cleanly")

	root, err := RepoRoot()
	require.NoError(t, err)
	pages, issues := Render(root, DefaultCatalog)
	require.Empty(t, issues, "a written map has no issue to report")
	for path, want := range pages {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err, path)
		assert.Equal(t, want, string(got), "%s after RunAgentsMap is the page Render builds", path)
	}
}

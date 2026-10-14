package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheMapLinksTheStandardAndDoesNotEmbedIt holds the v1.3 change: the root
// AGENTS.md carries the directory map and a link to docs/STANDARD.md, not the
// standard's body (docs/STANDARD.md section 7: less code is the best code; the
// standard is one source, not a copy).
func TestTheMapLinksTheStandardAndDoesNotEmbedIt(t *testing.T) {
	t.Parallel()

	root, err := docs.RepoRoot()
	require.NoError(t, err)
	standard, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(docs.StandardDoc)))
	require.NoError(t, err, "%s: %v; the standard is the one source the map links", docs.StandardDoc, err)
	pages, issues := docs.Render(root, docs.DefaultCatalog)
	require.Empty(t, issues, "render: %s", strings.Join(issues, "; "))

	body := pages[docs.RootAgents]

	assert.Contains(t, body, "["+docs.StandardDoc+"]("+docs.StandardDoc+")",
		"%s does not link the standard", docs.RootAgents)
	for _, line := range strings.Split(strings.TrimRight(string(standard), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		assert.NotContains(t, body, line,
			"%s still embeds this line of %s: %q; the map must link the standard, not carry its body", docs.RootAgents, docs.StandardDoc, line)
	}
}

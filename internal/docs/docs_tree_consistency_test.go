package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDocsTreeIsConsistent checks every Markdown page under docs/, including
// nested guides that the top-level link check does not visit. Relative links
// must resolve to files and their anchors must name headings in those files.
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	docsRoot := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checkReadmeLinks(t, path, string(body))
		return nil
	})
	require.NoError(t, err, "walk docs tree")
}

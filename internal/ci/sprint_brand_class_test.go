package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSprintBrandSheetIsCompleteAndEveryAssetHasProvenance pins the sprint's
// brand sheet and the provenance row for every asset below assets/sprint.
func TestSprintBrandSheetIsCompleteAndEveryAssetHasProvenance(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	brandPath := filepath.Join(root, "docs", "sprint", "BRAND.md")
	brand, err := os.ReadFile(brandPath)
	require.NoError(t, err, "the nova-sprint brand sheet is missing")
	for _, heading := range []string{"## Name", "## Mark", "## Colours", "## Type", "## Voice"} {
		assert.Containsf(t, string(brand), heading, "%s needs a %q section", brandPath, heading)
	}

	assetsDir := filepath.Join(root, "assets", "sprint")
	var assets []string
	err = filepath.WalkDir(assetsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		assets = append(assets, filepath.ToSlash(rel))
		return nil
	})
	require.NoError(t, err, "walk assets/sprint")
	require.NotEmpty(t, assets, "assets/sprint has no original brand asset")

	provenancePath := filepath.Join(root, "docs", "ASSET-PROVENANCE.md")
	provenance, err := os.ReadFile(provenancePath)
	require.NoError(t, err, "read asset provenance")
	for _, asset := range assets {
		assert.Containsf(t, string(provenance), "`"+asset+"`", "add a provenance row for %s", asset)
	}
}

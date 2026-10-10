package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: THE SPRINT BRAND SHEET IS COMPLETE (docs/SPEC-CI.md, `brand`).
//
// The public sprint identity has a name, a mark, colours, type and voice. Each
// original file under assets/sprint/ has a matching provenance row so a reader
// knows how it came to be.
func TestSprintBrandSheetIsCompleteAndEveryAssetHasProvenance(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	brandPath := filepath.Join(root, "docs", "sprint", "BRAND.md")
	brand, err := os.ReadFile(brandPath)
	require.NoError(t, err, "docs/sprint/BRAND.md must define the sprint identity")
	for _, heading := range []string{"Name", "Mark", "Colours", "Type", "Voice", "Do and don't"} {
		assert.Contains(t, string(brand), "## "+heading, "BRAND.md needs a %q section", heading)
	}

	assetsDir := filepath.Join(root, "assets", "sprint")
	var assets []string
	err = filepath.WalkDir(assetsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			assets = append(assets, filepath.ToSlash(rel))
		}
		return nil
	})
	require.NoError(t, err, "assets/sprint/ must exist and be readable")
	require.NotEmpty(t, assets, "the sprint mark or header artwork belongs under assets/sprint/")

	provenancePath := filepath.Join(root, "docs", "ASSET-PROVENANCE.md")
	provenance, err := os.ReadFile(provenancePath)
	require.NoError(t, err, "docs/ASSET-PROVENANCE.md must record every sprint asset")
	for _, asset := range assets {
		row := "| `" + asset + "` |"
		assert.True(t, strings.Contains(string(provenance), row), "docs/ASSET-PROVENANCE.md needs a row for %s", asset)
	}
}

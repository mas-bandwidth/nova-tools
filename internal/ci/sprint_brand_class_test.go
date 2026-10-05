package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: THE NOVA SPRINT BRAND SHEET IS COMPLETE.
//
// The sprint brand card makes Name, Mark, Colours, Type and Voice the reusable
// source for its documentation suite. ASSET-PROVENANCE.md requires one exact
// row per asset, so generated art never enters the tree without its origin.
func TestSprintBrandSheetIsCompleteAndEveryAssetHasProvenance(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	brandPath := filepath.Join(root, "docs", "sprint", "BRAND.md")
	brand, err := os.ReadFile(brandPath)
	require.NoError(t, err, "the Nova Sprint brand sheet is missing: %s", brandPath)

	for _, heading := range []string{"Name", "Mark", "Colours", "Type", "Voice"} {
		assert.Regexp(t, regexp.MustCompile(`(?m)^## `+regexp.QuoteMeta(heading)+`[ \t]*$`), string(brand),
			"docs/sprint/BRAND.md needs the exact second-level heading %q", heading)
	}

	var assets []string
	assetRoot := filepath.Join(root, "assets", "sprint")
	err = filepath.WalkDir(assetRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			assets = append(assets, filepath.ToSlash(rel))
		}
		return nil
	})
	require.NoError(t, err, "walk Nova Sprint assets")
	require.NotEmpty(t, assets, "assets/sprint needs the Nova Sprint mark or header artwork")
	sort.Strings(assets)

	provenancePath := filepath.Join(root, "docs", "ASSET-PROVENANCE.md")
	provenance, err := os.ReadFile(provenancePath)
	require.NoError(t, err, "read asset provenance")
	rows := provenanceFiles(string(provenance))
	for _, asset := range assets {
		assert.Contains(t, rows, asset, "%s needs its exact file row in docs/ASSET-PROVENANCE.md", asset)
	}
}

// provenanceFiles reads the first cell of each Markdown table row. Exact cells
// prevent one asset name from satisfying another asset's provenance rule.
func provenanceFiles(markdown string) map[string]bool {
	files := map[string]bool{}
	for _, line := range strings.Split(markdown, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 || strings.TrimSpace(cells[2]) == "" {
			continue
		}
		name := strings.TrimSpace(cells[1])
		if len(name) >= 2 && name[0] == '`' && name[len(name)-1] == '`' {
			files[strings.Trim(name, "`")] = true
		}
	}
	return files
}

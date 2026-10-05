package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDocsCards verifies that docs/security2.md and docs/coverage2.md track
// the expected cards held back until #5293 merges.
func TestDocsCards(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	tests := []struct {
		name      string
		relPath   string
		wantCards []string
	}{
		{
			name:    "security2",
			relPath: filepath.Join("docs", "security2.md"),
			wantCards: []string{
				"- security/blocked-5293/fp-sec67-f1.md",
				"- security/blocked-5293/fp-sec67-f2.md",
			},
		},
		{
			name:    "coverage2",
			relPath: filepath.Join("docs", "coverage2.md"),
			wantCards: []string{
				"coverage-wave2/blocked-5293/cover-cmd-nova-sandbox-main.md",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join(root, tc.relPath))
			require.NoError(t, err)

			content := string(data)
			for _, card := range tc.wantCards {
				assert.Contains(t, content, card)
			}
		})
	}
}

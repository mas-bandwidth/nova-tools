package cardcontract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reproduced by Stella on probe branch stella/review-4969-followup (d7b37ac55).
// A staged path and every component below the recipes root must stay in that tree
// (docs/SPEC-CARD-CONTRACT.md, Staged recipes).
func TestStageRecipesRejectsSymlinkParents(t *testing.T) {
	t.Parallel()

	t.Run("source parent", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		recipes := filepath.Join(root, "recipes")
		outside := filepath.Join(root, "outside")
		job := filepath.Join(root, "job")
		require.NoError(t, os.MkdirAll(recipes, 0o755))
		require.NoError(t, os.MkdirAll(outside, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside recipe"), 0o600))
		require.NoError(t, os.Symlink(outside, filepath.Join(recipes, "alias")))

		err := StageRecipes(Frame{Stage: []string{"alias/secret.md"}, Recipes: recipes}, job)
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(job, RecipesName, "alias", "secret.md"))
	})

	t.Run("ordinary nested recipe remains stageable", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		recipes := filepath.Join(root, "recipes")
		job := filepath.Join(root, "job")
		want := []byte("ordinary nested recipe")
		require.NoError(t, os.MkdirAll(filepath.Join(recipes, "pr"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(recipes, "pr", "4926.md"), want, 0o600))

		require.NoError(t, StageRecipes(Frame{Stage: []string{"pr/4926.md"}, Recipes: recipes}, job))
		got, err := os.ReadFile(filepath.Join(job, RecipesName, "pr", "4926.md"))
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
}

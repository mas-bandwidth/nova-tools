package cardcontract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cardURL is the card's repository as the frame names it; the shims never reach it (a
// clone of it is a link, a push is a line), so it is a name only.
const cardURL = "https://example.com/Example-Owner/example-repo.git"

func TestJobTextCarriesTheAttemptBefore(t *testing.T) {
	t.Parallel()
	f := Frame{Kind: "work", Card: "c1.w2", Attempt: 2, Tier: "pro", Repo: cardURL, BaseRef: "main", Branch: "sprint/c1.w2",
		PrevHead: "0123456789abcdef0123456789abcdef01234567", Finding: "f.go:12 the bound is not asserted"}
	s := Staged{Job: "/j", Repo: "/j/repo", Head: "0123456789abcdef0123456789abcdef01234567"}
	for _, family := range []string{"claude", "plain"} {
		text := For(family).JobText(f, s)
		for _, want := range []string{"Attempt 2", "previous attempt's head is 0123456789abcdef0123456789abcdef01234567", "f.go:12 the bound is not asserted", "Tier: pro."} {
			assert.Contains(t, text, want, family)
		}
	}
	f.Attempt = 1
	assert.Contains(t, For("claude").JobText(f, s), `gh pr create --title "nothing: <why>"`)
	assert.Contains(t, For("claude").JobText(f, s), "there is nothing else to write")
	assert.Contains(t, For("plain").JobText(f, s), "verdict: ok | not-done | nothing")
	f.Kind = "read"
	assert.Contains(t, For("claude").JobText(f, s), "gh pr review --request-changes")
	assert.Contains(t, For("plain").JobText(f, s), "verdict: ok | broken")
}

func TestFamilyOfAModelId(t *testing.T) {
	t.Parallel()
	for model, family := range map[string]string{
		"anthropic/claude-opus-5-5": "claude", "claude-sonnet": "claude", "openai/gpt-6": "openai",
		"google/gemini-4-argon": "gemini", "xai/grok-5": "grok", "deepseek/deepseek-chat": "deepseek",
		"local/qwen3": "plain", "": "plain",
	} {
		assert.Equal(t, family, FamilyOf(model), model)
	}
	for _, f := range Families {
		assert.Equal(t, f, For(f).Family())
	}
}

func TestAFrameRoundTripsAndIsRefusedWithoutAKind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := Frame{Kind: "work", Card: "c1", Attempt: 2, Repo: cardURL, StageSha: "0123456789abcdef0123456789abcdef01234567"}
	p := filepath.Join(dir, "f.json")
	require.NoError(t, WriteFrame(p, f))
	got, err := ReadFrame(p)
	require.NoError(t, err)
	assert.Equal(t, f, got)
	require.NoError(t, WriteFrame(p, Frame{Card: "c1"}))
	_, err = ReadFrame(p)
	assert.ErrorContains(t, err, "wants kind work or read")
	assert.Equal(t, "Read /j/JOB.md first.\n\ncard", Prompt("/j", "card"))
}

// A brief's Stage: recipes are copied from the member's recipes directory into
// <job>/recipes at their relative paths; a name outside the directory, a link
// or a missing file refuses the staging (docs/SPEC-CARD-CONTRACT.md).
func TestStageRecipesCopiesOnlyFilesInTheRecipesDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipes, job := filepath.Join(root, "recipes"), filepath.Join(root, "job")
	require.NoError(t, os.MkdirAll(filepath.Join(recipes, "pr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipes, "pr", "4926.md"), []byte("the rewriter"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.md"), []byte("not a recipe"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(root, "outside.md"), filepath.Join(recipes, "link.md")))

	require.NoError(t, StageRecipes(Frame{Stage: []string{"pr/4926.md"}, Recipes: recipes}, job))
	b, err := os.ReadFile(filepath.Join(job, RecipesName, "pr", "4926.md"))
	require.NoError(t, err)
	assert.Equal(t, "the rewriter", string(b))
	for _, rel := range []string{"../outside.md", "/etc/hosts", "link.md", "missing.md"} {
		assert.Error(t, StageRecipes(Frame{Stage: []string{rel}, Recipes: recipes}, job), rel)
	}
	text := For("claude").JobText(Frame{Kind: "work", Stage: []string{"pr/4926.md"}}, Staged{Job: "/j"})
	assert.Contains(t, text, "Staged for you in /j/recipes: pr/4926.md.")
}

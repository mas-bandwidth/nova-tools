package memindex

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetrievalKeepsSourceLinesAndOriginalText(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(ending, func(t *testing.T) {
			t.Parallel()
			text := "---\nname: rocket\ntype: reference\n---\n\n# Heading\n\n\nThe **Rocket Engine** burns hot fuel daily.\nIts flame is Bright.\n\nA second rocket paragraph burns cooler fuel."
			c, err := Build(fstest.MapFS{"a.md": {Data: []byte(strings.ReplaceAll(text, "\n", ending))}}, nil)
			require.NoError(t, err)
			for _, ch := range c.Chunks {
				assert.Equal(t, Normalize(ch.Original), ch.Text)
			}
			hits := Retrieve(c, []Channel{NewBM25(c)}, "bright", 1)
			require.Len(t, hits, 1)
			assert.Equal(t, 9, hits[0].Line)
			assert.Equal(t, "The **Rocket Engine** burns hot fuel daily.\nIts flame is Bright.", hits[0].Snippet)
			assert.Equal(t, 0, hits[0].Para, "the frontmatter is no paragraph and the heading is under MinTerms, so this is paragraph 0")
			trigram := Retrieve(c, []Channel{NewTrigram(c)}, "rocket engine burns hot fuel daily", 1)
			require.Len(t, trigram, 1)
			assert.Equal(t, hits[0].Para, trigram[0].Para)
		})
	}
}

// TestBuildRefusesAMarkdownSymlinkLeaf pins the walk-time type check on
// markdown leaves: a .md name is not a .md file, and os.DirFS follows a
// leaf symlink on open, so a link pointing at a regular file outside the
// root was indexed as corpus — its text surfacing as hits, its bytes in
// stats. Build refuses such a leaf before any open, naming the path and
// the exclusion that keeps the rest of the corpus building (security#76
// finding 1, re-filed from security#58 finding 1).
func TestBuildRefusesAMarkdownSymlinkLeaf(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a.md":          {Data: []byte("# A\n\nThe one regular file in the corpus.\n")},
		"notes/link.md": {Data: []byte("../a.md"), Mode: fs.ModeSymlink},
	}
	t.Run("a symlink leaf is refused before any open", func(t *testing.T) {
		t.Parallel()
		c, err := Build(fsys, nil)
		require.Error(t, err, "before the walk-time check the link was indexed as corpus: no error")
		assert.ErrorContains(t, err, "notes/link.md")
		assert.ErrorContains(t, err, "is not a regular file")
		assert.Nil(t, c, "a refused build returns no corpus")
	})
	t.Run("an excluded symlink is the remedy the refusal names", func(t *testing.T) {
		t.Parallel()
		c, err := Build(fsys, func(p string) bool { return p == "notes/link.md" })
		require.NoError(t, err)
		assert.Equal(t, []string{"a.md"}, c.Files, "the regular file beside the link is still indexed")
	})
}

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

// TestBuildNeverOpensANamedPipe: a FIFO named pipe.md passes the .md suffix
// test and, without the walk-time type check, fs.ReadFile opens it and blocks
// forever with no writer — every verb that builds the index hangs
// (security#76 finding 2, re-filed from security#58 finding 2). The walk
// refuses it as not a regular file before any open; the wrapper's Open for
// pipe.md fails the test, so a regression to opening it cannot slip through.
func TestBuildNeverOpensANamedPipe(t *testing.T) {
	t.Parallel()

	mapped := fstest.MapFS{
		"a.md":    &fstest.MapFile{Data: []byte("# a page\n\nbody one\n")},
		"pipe.md": &fstest.MapFile{Data: []byte("never read\n"), Mode: fs.ModeNamedPipe},
	}
	opened := false
	fsys := openingFS{FS: mapped, onOpen: func(name string) {
		if name == "pipe.md" {
			opened = true
		}
	}}
	_, err := Build(fsys, nil)
	require.Error(t, err, "Build over a corpus holding a named pipe: err %v", err)
	require.False(t, opened, "pipe.md was opened; the walk should refuse it before any open")
	assert.Contains(t, err.Error(), "pipe.md", "the refusal names the file: %v", err)
	assert.Contains(t, err.Error(), "not a regular file", "the refusal names the defect: %v", err)
}

// openingFS wraps an fs.FS and tells its onOpen about every file opened.
type openingFS struct {
	fs.FS
	onOpen func(name string)
}

func (o openingFS) Open(name string) (fs.File, error) {
	if o.onOpen != nil {
		o.onOpen(name)
	}
	return o.FS.Open(name)
}

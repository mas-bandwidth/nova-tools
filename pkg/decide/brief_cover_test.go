package decide

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card files of a directory are its non-directory *.md entries, in byte
// order of name, none below it (brief.go, nova-sprint add --brief-dir); a
// directory that cannot be read is the error.
func TestBriefCoverCardFilePathsListsTheMarkdownEntries(t *testing.T) {
	t.Parallel()
	t.Run("the non-directory *.md entries in byte order of name, none below", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("card b"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("card a"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a card"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "d.md"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "d.md", "below.md"), []byte("not read"), 0o600))
		paths, err := CardFilePaths(dir)
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")}, paths,
			"byte order of name, directories and non-.md entries and anything below left out")
	})
	t.Run("refusal: a directory that cannot be read", func(t *testing.T) {
		t.Parallel()
		_, err := CardFilePaths(filepath.Join(t.TempDir(), "absent"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

// CardFiles reads one card file, or a directory's cards (add --brief-dir's): a
// card's id is its file's name without .md and its brief the file's text with
// one trailing newline cut; an unreadable path, or a directory holding no *.md
// card file, is the error.
func TestBriefCoverCardFilesReadsAFileOrADirectory(t *testing.T) {
	t.Parallel()
	t.Run("a file is one card with one trailing newline cut", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "one.md")
		require.NoError(t, os.WriteFile(path, []byte("the card\n\n"), 0o600))
		cards, err := CardFiles(path)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"one": "the card\n"}, cards)
	})
	t.Run("a directory is its *.md entries as id to brief", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("card a"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("card b\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte("not a card"), 0o600))
		cards, err := CardFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"a": "card a", "b": "card b"}, cards)
	})
	t.Run("refusals: an unreadable path, an unreadable entry, a directory with no *.md card", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "only.txt"), []byte("not a card"), 0o600))
		_, err := CardFiles(filepath.Join(dir, "absent.md"))
		assert.ErrorIs(t, err, fs.ErrNotExist, "a path that cannot be read is the error")
		sub := filepath.Join(t.TempDir(), "sub")
		require.NoError(t, os.Mkdir(sub, 0o700))
		require.NoError(t, os.Symlink(sub, filepath.Join(dir, "link.md")))
		_, err = CardFiles(dir)
		assert.Error(t, err, "a *.md entry that cannot be read is the error")
		dir2 := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir2, "only.txt"), []byte("not a card"), 0o600))
		_, err = CardFiles(dir2)
		assert.ErrorContains(t, err, "holds no *.md card file", "an empty directory is refused, not an empty brief")
		closed := filepath.Join(t.TempDir(), "locked")
		require.NoError(t, os.Mkdir(closed, 0o000))
		_, err = CardFiles(closed)
		assert.ErrorIs(t, err, fs.ErrPermission, "a directory stat sees but cannot read is the error")
	})
}

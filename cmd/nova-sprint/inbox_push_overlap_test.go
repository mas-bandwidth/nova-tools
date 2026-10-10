package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteOnceLeavesAFilePublishedByAnotherWriter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")

	// First writer writes "A" and simulates being stopped between its Link and Remove.
	require.NoError(t, os.WriteFile(path+".tmp", []byte("A"), 0o644))
	require.NoError(t, os.Link(path+".tmp", path))

	// Second writer tries to write "B".
	wrote, err := writeOnce(path, "B")
	require.False(t, wrote)
	require.Nil(t, err)

	// The original file is unchanged.
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "A", string(contents))

	// The temporary file still exists and still reads "A".
	tmpContents, err := os.ReadFile(path + ".tmp")
	require.NoError(t, err)
	require.Equal(t, "A", string(tmpContents))
}

func TestWriteOnceWritesANewFileWholeAndLeavesNoTemporaryName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")

	wrote, err := writeOnce(path, "whole text")
	require.True(t, wrote)
	require.Nil(t, err)

	// Check that file has whole content.
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "whole text", string(contents))

	// No temporary file remains.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "note.md", entries[0].Name())
}

func TestWriteOnceNeverReplacesAnExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")

	// Create an existing file.
	require.NoError(t, os.WriteFile(path, []byte("existing"), 0o644))

	wrote, err := writeOnce(path, "new")
	require.False(t, wrote)
	require.Nil(t, err)

	// The existing file is unchanged.
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing", string(contents))
}

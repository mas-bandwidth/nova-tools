package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDraftCoverIsPathExistPinsShape pins isPathExist: a path that is there
// (a regular file or a directory) returns true, and a path that is not there
// returns false. The function is a one-line os.Lstat check with no network,
// no subprocess, no sleeps and no store, so every case is driven through
// t.TempDir only. The refusal row -- the path that is not there -- is the
// one call-site in writeSkeleton that reaches this helper when OpenFile
// with O_EXCL fails but os.ErrExist is not the error, a race that cannot be
// reproduced with a fake and so is covered at the predicate instead.
func TestDraftCoverIsPathExistPinsShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	existingFile := filepath.Join(dir, "note.md")
	require.NoError(t, os.WriteFile(existingFile, []byte("{}"), 0o644))
	existingDir := filepath.Join(dir, "subdir")
	require.NoError(t, os.MkdirAll(existingDir, 0o755))

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"regular file exists", existingFile, true},
		{"directory exists", existingDir, true},
		{"nonexistent path is false", filepath.Join(dir, "nope.md"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isPathExist(tc.path), "isPathExist(%q)", tc.path)
		})
	}
}

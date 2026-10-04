package fuse

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBoxRefusesSymlinkParent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege is not available on all Windows runners")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	err := os.Mkdir(real, 0700)
	require.NoError(t, err)
	err = os.Symlink(real, link)
	require.NoError(t, err)
	err = CreateBox(filepath.Join(link, "box.json"))
	assert.Error(t, err, "CreateBox accepted a symlink parent")
	entries, err := os.ReadDir(real)
	require.NoError(t, err)
	assert.Len(t, entries, 0, "refusal left entries in the linked directory: %v", entries)
}

func TestConcurrentCreateBoxHasOneWinner(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "box.json")
	var wg sync.WaitGroup
	var wins atomic.Int32
	for range 16 {
		wg.Go(func() {
			err := CreateBox(path)
			if err == nil {
				wins.Add(1)
			} else {
				assert.ErrorIs(t, err, fs.ErrExist, "create: %v", err)
			}
		})
	}
	wg.Wait()
	n := wins.Load()
	require.Equal(t, int32(1), n, "successful creators=%d, want1", n)
	box, err := ReadBox(path)
	require.NoError(t, err)
	require.Nil(t, box.Lockdown, "created box not empty: %+v", box)
	require.Empty(t, box.Quarantine, "created box not empty: %+v", box)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "creation left temp files: %v", entries)
}

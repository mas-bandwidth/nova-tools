package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNovaDeleteRefusesAnythingButOneLiteralPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	os.Setenv("NOVA_DELETE_ROOTS", tmpDir)
	defer os.Unsetenv("NOVA_DELETE_ROOTS")

	tests := []struct {
		name     string
		path     string
		wantExit int
	}{
		{"empty argument", "", 2},
		{"glob star", "/tmp/*", 2},
		{"glob question", "/tmp?", 2},
		{"glob bracket", "/tmp[", 2},
		{"backtick", "/tmp`cmd`", 2},
		{"relative path", "./tmp/file", 2},
		{"root filesystem", "/", 2},
		{"temp root itself", os.TempDir(), 2},
		{"home root itself", os.Getenv("HOME"), 2},
		{"outside allowed roots", "/etc/hosts", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, exit, err := Delete(tt.path)
			require.Equal(t, tt.wantExit, exit)
			require.Error(t, err)
		})
	}
}

func TestNovaDeleteMovesLiteralPath(t *testing.T) {
	t.Parallel()

	tmpRoot := t.TempDir()
	os.Setenv("NOVA_DELETE_ROOTS", tmpRoot)
	defer os.Unsetenv("NOVA_DELETE_ROOTS")

	filePath := filepath.Join(tmpRoot, "testfile.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("test"), 0644))

	msg, exit, err := Delete(filePath)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
	require.Contains(t, msg, "MOVED ")
	_, err = os.Stat(filePath)
	require.Error(t, err)

	dirPath := filepath.Join(tmpRoot, "testdir")
	require.NoError(t, os.MkdirAll(dirPath, 0755))
	testFile := filepath.Join(dirPath, "nested.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("nested"), 0644))

	msg, exit, err = Delete(dirPath)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
	require.Contains(t, msg, "MOVED ")
	_, err = os.Stat(dirPath)
	require.Error(t, err)

	missingPath := filepath.Join(tmpRoot, "nonexistent.txt")
	msg, exit, err = Delete(missingPath)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
	require.Contains(t, msg, "SKIP ")
}

func TestNovaDeleteSweep(t *testing.T) {
	t.Parallel()

	tmpRoot := t.TempDir()
	os.Setenv("NOVA_DELETE_ROOTS", tmpRoot)
	defer os.Unsetenv("NOVA_DELETE_ROOTS")

	oldDir := filepath.Join(tmpRoot, ".quarantine-20200101")
	require.NoError(t, os.MkdirAll(oldDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "old.txt"), []byte("old"), 0644))
	oldTime := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(oldDir, oldTime, oldTime))

	recentDir := filepath.Join(tmpRoot, ".quarantine-"+time.Now().Format("20060102"))
	require.NoError(t, os.MkdirAll(recentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(recentDir, "recent.txt"), []byte("recent"), 0644))

	swept, err := Sweep(7 * 24 * time.Hour)
	require.NoError(t, err)
	require.Len(t, swept, 1)
	_, err = os.Stat(oldDir)
	require.Error(t, err)
	_, err = os.Stat(recentDir)
	require.NoError(t, err)
}

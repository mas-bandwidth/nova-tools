package up_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/up"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpOsexecCoverLastLine covers the lastLine helper function.
func TestUpOsexecCoverLastLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"multi-line", "a\nb\n", "b"},
		{"single-line", "one", "one"},
		{"only-whitespace", "  \n", ""},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := up.LastLine(c.in)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestUpOsexecCoverLocal covers the Local function.
func TestUpOsexecCoverLocal(t *testing.T) {
	t.Parallel()
	machine, err := up.Local()
	require.NoError(t, err)
	assert.Equal(t, runtime.GOOS, machine.GOOS)
	require.NotNil(t, machine.Exec)
	require.NotNil(t, machine.Now)
	require.NotNil(t, machine.Rand)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, home, machine.Home)
}

// TestUpOsexecCoverRunMissing covers osExec.Run when the program path does not exist.
func TestUpOsexecCoverRunMissing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing")
	_, err := up.Osexec{}.Run(context.Background(), up.Cmd{Name: path})
	require.Error(t, err)
	var exitErr *exec.ExitError
	assert.False(t, errors.As(err, &exitErr))
	assert.Contains(t, err.Error(), "missing")
}

// TestUpOsexecCoverLookPath covers osExec.LookPath.
func TestUpOsexecCoverLookPath(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(tmpDir, "testbin")
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho test\n"), 0o755))
		got, err := up.Osexec{}.LookPath(path)
		require.NoError(t, err)
		assert.Equal(t, path, got)
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := up.Osexec{}.LookPath(filepath.Join(tmpDir, "does-not-exist"))
		require.Error(t, err)
	})
}

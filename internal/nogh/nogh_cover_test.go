package nogh

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoghCoverScript(t *testing.T) {
	t.Parallel()

	script := Script()
	rows := []struct {
		name string
		want string
	}{
		{name: "shebang", want: "#!/bin/sh\n"},
		{name: "echoes the refusal to stderr", want: "echo '" + Refusal + "' >&2\n"},
		{name: "exits two", want: "exit 2\n"},
		{name: "carries the refusal's reason", want: "GitHub is a git remote only"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, script, row.want, "Script = %q", script)
		})
	}
	t.Run("the refusal quotes safely in sh", func(t *testing.T) {
		t.Parallel()
		assert.NotContains(t, Refusal, "'", "a single quote breaks Script's echo line")
	})
}

func TestNoghCoverInstall(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("a file where a directory is wanted"), 0o644))

	rows := []struct {
		name    string
		dir     func(t *testing.T) string
		wantErr string
	}{
		{
			name: "installs the refusing gh",
			dir:  func(t *testing.T) string { return t.TempDir() },
		},
		{
			name:    "refuses a directory it cannot make",
			dir:     func(t *testing.T) string { return filepath.Join(blocker, "bin") },
			wantErr: "the gh shim directory",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := row.dir(t)
			path, err := Install(dir)
			if row.wantErr != "" {
				assert.ErrorContains(t, err, row.wantErr, "Install(%s) = %q, %v", dir, path, err)
				assert.Empty(t, path, "a refused Install returns no path")
				return
			}
			require.NoError(t, err, "Install(%s) = %q, %v", dir, path, err)
			assert.Equal(t, filepath.Join(dir, Name), path, "Install returns the shim's path")
			text, err := os.ReadFile(path)
			require.NoError(t, err, "ReadFile %s: %v", path, err)
			assert.Equal(t, Script(), string(text), "the shim's text is Script()")
			if runtime.GOOS != "windows" {
				info, err := os.Stat(path)
				require.NoError(t, err, "Stat %s: %v", path, err)
				assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "the shim's mode")
			}
		})
	}
}

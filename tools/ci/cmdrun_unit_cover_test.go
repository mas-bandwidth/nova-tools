package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCiCmdrunCoverLongRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prog string
		args []string
		want bool
	}{
		{"make", nil, true},
		{"apt-get", nil, true},
		{"brew", nil, true},
		{"sbcl", nil, true},
		{"nova-secrets", nil, true},
		{"sudo", nil, true},
		{"/usr/bin/make", nil, true},
		{"go", []string{"test"}, true},
		{"go", []string{"build"}, true},
		{"go", []string{"run"}, true},
		{"go", []string{"install"}, true},
		{"go", []string{"vet"}, true},
		{"go", []string{"list"}, false},
		{"go", []string{"version"}, false},
		{"go", nil, false},
		{"gh", nil, false},
		{"curl", nil, false},
		{"tar", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.prog+" "+strings.Join(tt.args, " "), func(t *testing.T) {
			t.Parallel()
			got := longRunning(tt.prog, tt.args...)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCiCmdrunCoverRun(t *testing.T) {
	t.Parallel()

	t.Run("empty name", func(t *testing.T) {
		t.Parallel()
		o := &osCmdRunner{}
		code, err := o.Run()
		assert.Equal(t, -1, code)
		assert.Error(t, err)
	})

	t.Run("missing program", func(t *testing.T) {
		t.Parallel()
		o := &osCmdRunner{}
		dir := t.TempDir()
		name := "git"
		_, err := o.Run(filepath.Join(dir, name))
		assert.Error(t, err)
		assert.NotErrorIs(t, err, (*exec.ExitError)(nil))
		assert.NotErrorIs(t, err, (*os.PathError)(nil))
	})
}

func TestCiCmdrunCoverLookPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create an executable
	execPath := filepath.Join(dir, "testexe")
	f, err := os.Create(execPath)
	require.NoError(t, err)
	f.Close()
	os.Chmod(execPath, 0o755)

	// Should find it
	got, err := LookPath(execPath)
	require.NoError(t, err)
	assert.Equal(t, execPath, got)

	// Should not find missing
	_, err = LookPath(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

func TestCiCmdrunCoverPrependPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	PrependPath("/new")

	got := os.Getenv("PATH")
	assert.True(t, strings.HasPrefix(got, "/new"))
	assert.Contains(t, got, dir)
}

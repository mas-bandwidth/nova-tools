package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/require"
)

func TestCiCmdrunCoverLongRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		spec     cmdSpec
		expected bool
	}{
		{"make", cmdSpec{Name: "make"}, true},
		{"apt-get", cmdSpec{Name: "apt-get"}, true},
		{"brew", cmdSpec{Name: "brew"}, true},
		{"sbcl", cmdSpec{Name: "sbcl"}, true},
		{"nova-secrets", cmdSpec{Name: "nova-secrets"}, true},
		{"sudo", cmdSpec{Name: "sudo"}, true},
		{"absolute /usr/bin/make", cmdSpec{Name: "/usr/bin/make"}, true},
		{"go test", cmdSpec{Name: "go", Args: []string{"test"}}, true},
		{"go build", cmdSpec{Name: "go", Args: []string{"build"}}, true},
		{"go run", cmdSpec{Name: "go", Args: []string{"run"}}, true},
		{"go install", cmdSpec{Name: "go", Args: []string{"install"}}, true},
		{"go vet", cmdSpec{Name: "go", Args: []string{"vet"}}, true},
		{"go list", cmdSpec{Name: "go", Args: []string{"list"}}, false},
		{"go version", cmdSpec{Name: "go", Args: []string{"version"}}, false},
		{"bare go", cmdSpec{Name: "go"}, false},
		{"gh", cmdSpec{Name: "gh"}, false},
		{"curl", cmdSpec{Name: "curl"}, false},
		{"tar", cmdSpec{Name: "tar"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, longRunning(tc.spec))
		})
	}
}

func TestCiCmdrunCoverRunEmptyName(t *testing.T) {
	t.Parallel()

	r := osCmdRunner{}
	code, err := r.Run(cmdSpec{Name: ""})

	require.Equal(t, -1, code)
	require.Error(t, err)
	require.Equal(t, "no command", err.Error())
}

func TestCiCmdrunCoverRunNonExistent(t *testing.T) {
	t.Parallel()

	r := osCmdRunner{}
	tmpDir := t.TempDir()

	for _, name := range []string{"git", "make", "curl"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(tmpDir, name)
			code, err := r.Run(cmdSpec{Name: path})

			require.Equal(t, -1, code)
			require.Error(t, err)
			var exitErr *exec.ExitError
			var timeoutErr *subproc.TimeoutError
			require.False(t, errors.As(err, &exitErr))
			require.False(t, errors.As(err, &timeoutErr))
		})
	}
}

func TestCiCmdrunCoverLookPath(t *testing.T) {
	t.Parallel()

	r := osCmdRunner{}
	tmpDir := t.TempDir()

	// Create an executable file
	execPath := filepath.Join(tmpDir, "fakeexec")
	f, err := os.Create(execPath)
	require.NoError(t, err)
	_, err = f.WriteString("#!/bin/sh\necho hello\n")
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)
	err = os.Chmod(execPath, 0o755)
	require.NoError(t, err)

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		path, err := r.LookPath(execPath)
		require.NoError(t, err)
		require.Equal(t, execPath, path)
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := r.LookPath(filepath.Join(tmpDir, "doesnotexist"))
		require.Error(t, err)
	})
}

func TestCiCmdrunCoverPrependPath(t *testing.T) {
	// Do NOT use t.Parallel because we modify the process environment
	tmpDir := t.TempDir()
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+oldPath)
	r := osCmdRunner{}
	r.PrependPath(tmpDir)
	newPath := os.Getenv("PATH")
	require.Equal(t, tmpDir+string(os.PathListSeparator)+oldPath, newPath)
}

package main

// cmdrun_unit_cover_test.go drives the real osCmdRunner, not the fake: every verb
// test runs through cmdrun_fake_test.go, so the real runner's choice of door (git
// through gitrun, a long-running child through subproc.Long, any other program
// through subproc.Prepare), its empty-name refusal and its PATH handling are
// pinned here. A program path that does not exist fails at start on each door, so
// no child is ever created; these tests start no process, keep no live store and
// touch no network.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCiCmdrunCoverLongRunning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		spec cmdSpec
		want bool
	}{
		{"make", cmdSpec{Name: "make"}, true},
		{"apt-get", cmdSpec{Name: "apt-get"}, true},
		{"brew", cmdSpec{Name: "brew"}, true},
		{"sbcl", cmdSpec{Name: "sbcl"}, true},
		{"nova-secrets", cmdSpec{Name: "nova-secrets"}, true},
		{"sudo", cmdSpec{Name: "sudo"}, true},
		{"absolute make", cmdSpec{Name: "/usr/bin/make"}, true},
		{"go test", cmdSpec{Name: "go", Args: []string{"test", "./..."}}, true},
		{"go build", cmdSpec{Name: "go", Args: []string{"build", "./..."}}, true},
		{"go run", cmdSpec{Name: "go", Args: []string{"run", "./cmd/nova-ci"}}, true},
		{"go install", cmdSpec{Name: "go", Args: []string{"install", "./..."}}, true},
		{"go vet", cmdSpec{Name: "go", Args: []string{"vet", "./..."}}, true},
		{"go list", cmdSpec{Name: "go", Args: []string{"list", "./..."}}, false},
		{"go version", cmdSpec{Name: "go", Args: []string{"version"}}, false},
		{"bare go", cmdSpec{Name: "go"}, false},
		{"gh", cmdSpec{Name: "gh"}, false},
		{"curl", cmdSpec{Name: "curl"}, false},
		{"tar", cmdSpec{Name: "tar"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, longRunning(tc.spec))
		})
	}
}

func TestCiCmdrunCoverRunRefusesAnEmptyName(t *testing.T) {
	t.Parallel()
	code, err := osCmdRunner{}.Run(cmdSpec{})
	require.Equal(t, -1, code)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no command")
}

// TestCiCmdrunCoverRunRefusesAMissingProgram walks the three doors of Run: git
// through gitrun, make as a long-running child through subproc.Long, and curl as
// an ordinary one-shot through subproc.Prepare. A path that is not there fails at
// start, so the child is never created and Run returns -1 with that start error,
// never an exit code and never a deadline.
func TestCiCmdrunCoverRunRefusesAMissingProgram(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"git", "make", "curl"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			missing := filepath.Join(t.TempDir(), name)
			code, err := osCmdRunner{}.Run(cmdSpec{Name: missing})
			require.Equal(t, -1, code)
			require.Error(t, err)
			var exit *exec.ExitError
			assert.False(t, errors.As(err, &exit), "a missing program is a start failure, not an exit: %v", err)
			var timeout *subproc.TimeoutError
			assert.False(t, errors.As(err, &timeout), "a missing program never reaches a deadline: %v", err)
		})
	}
}

func TestCiCmdrunCoverLookPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := filepath.Join(dir, "some-tool")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	got, err := osCmdRunner{}.LookPath(exe)
	require.NoError(t, err)
	assert.Equal(t, exe, got)
	_, err = osCmdRunner{}.LookPath(filepath.Join(dir, "absent-tool"))
	assert.Error(t, err)
}

// TestCiCmdrunCoverPrependPath changes this process's PATH, which is why it does
// not open with t.Parallel: t.Setenv forbids it. It pins that the new directory
// leads and the PATH it replaced follows the list separator.
func TestCiCmdrunCoverPrependPath(t *testing.T) {
	old := t.TempDir()
	t.Setenv("PATH", old)
	first := t.TempDir()
	osCmdRunner{}.PrependPath(first)
	assert.Equal(t, first+string(os.PathListSeparator)+old, os.Getenv("PATH"))
}

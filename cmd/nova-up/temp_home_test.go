package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain gives every test a private home and no XDG override, so a test that
// reads the process environment cannot touch the machine's real HOME (a unit
// test stays inside its working directory).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "nova-tools-up-home")
	if err != nil {
		panic(err)
	}
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	os.Unsetenv("XDG_CONFIG_HOME") // ignored: a fixed valid key; a refusal leaves it set and TestTheSuiteRunsWithATempHome reports it
	os.Unsetenv("XDG_CACHE_HOME")  // ignored: a fixed valid key; a failure only leaves an override in place for this short-lived test process
	code := m.Run()
	_ = os.Setenv("HOME", oldHome)
	os.RemoveAll(home) // ignored: the suite's exit code is already decided; a leftover temp directory is the OS's to reap
	os.Exit(code)
}

func TestTheSuiteRunsWithATempHome(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	rel, err := filepath.Rel(os.TempDir(), home)
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(rel, ".."), "HOME %q is not under TempDir %q", home, os.TempDir())
	_, set := os.LookupEnv("XDG_CONFIG_HOME")
	assert.False(t, set, "XDG_CONFIG_HOME must be unset")
}

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWallNoteStderrOutput(t *testing.T) {
	t.Parallel()

	// Create a temp dir to act as TMPDIR
	tmpdir := t.TempDir()

	// Create a script that prints a denial line to stderr and exits 1
	script := filepath.Join(tmpdir, "fake_denial.sh")
	err := os.WriteFile(script, []byte("#!/bin/sh\necho 'bash: line 1: /tmp/x: Permission denied' >&2\nexit 1\n"), 0o755)
	require.NoError(t, err)

	// Build wallnote
	wallnotePath := filepath.Join("..", "wallnote", "wallnote")
	if _, err := os.Stat(wallnotePath); os.IsNotExist(err) {
		// Build it if it doesn't exist
		cmd := exec.Command("go", "build", "-o", wallnotePath, ".")
		cmd.Dir = filepath.Join("..", "wallnote")
		cmd.Env = []string{"GOFLAGS=-mod=readonly"}
		require.NoError(t, cmd.Run())
	}

	// Set up environment with TMPDIR
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + tmpdir,
		"NOVA_SWARM_JOB=/jobs/a-card",
	}

	// Run wallnote with the fake script
	cmd := exec.Command(wallnotePath)
	cmd.Env = goenv.Clean(os.Environ())
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	// wallnote exits 0 (it just filters)
	require.NoError(t, err)

	// Check that stderr contains the NOVA WALL line
	output := stderr.String()
	assert.Contains(t, output, "NOVA WALL:", "should have NOVA WALL note for /tmp path")
}

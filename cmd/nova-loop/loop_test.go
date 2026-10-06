package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nova-loop's tests drive run with a fake command. None of them sleeps, and
// none of them replaces this process.

func TestLoopOneCopyRefusesALiveSecond(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var calls int
	start := func(argv []string) (bool, error) {
		calls++
		assert.Equal(t, []string{"true"}, argv)
		return true, nil
	}
	code, held := run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), start)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	t.Cleanup(func() {
		require.NoError(t, held.Unlock())
	})

	var stderr bytes.Buffer
	code, second := run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), &stderr, start)
	assert.Equal(t, 3, code)
	assert.Nil(t, second)
	assert.Contains(t, stderr.String(), "nova-loop BUSY ")
	assert.Contains(t, stderr.String(), "pid=")
	assert.Equal(t, 1, calls, "a live copy does not start the command")
}

func TestLoopTakesALockWhoseHolderIsDead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "demo.lock"), []byte("pid=999999\n"), 0o644))
	var stdout bytes.Buffer
	code, held := run(loopArgs(dir, "demo", "true"), &stdout, new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	t.Cleanup(func() { require.NoError(t, held.Unlock()) })
	assert.Contains(t, stdout.String(), "nova-loop OK name=demo restarts=1\n")
}

func TestLoopRefusesABadNameAndAMissingCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := [][]string{
		{"run", "--dir", dir, "--", "true"},
		{"run", "--name", "..", "--dir", dir, "--", "true"},
		{"run", "--name", "a/b", "--dir", dir, "--", "true"},
		{"run", "--name", "a..b", "--dir", dir, "--", "true"},
		{"run", "--name", "demo", "--dir", dir},
		{"fly"},
		{},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		code, held := run(args, &stdout, &stderr, okExec)
		assert.Equal(t, 2, code, "%q", args)
		assert.Nil(t, held, "%q", args)
		assert.Contains(t, stderr.String(), "nova-loop REFUSED: ", "%q", args)
		assert.Empty(t, stdout.String(), "%q", args)
	}
}

func TestLoopMetricsCountRestarts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, held := run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	require.NoError(t, held.Unlock())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "demo.prom"), []byte("garbage\n"), 0o644))
	code, held = run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	require.NoError(t, held.Unlock())
	raw, err := os.ReadFile(filepath.Join(dir, "demo.prom"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "nova_loop_restarts_total{loop=\"demo\"} 1\n")

	code, held = run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	require.NoError(t, held.Unlock())
	raw, err = os.ReadFile(filepath.Join(dir, "demo.prom"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "nova_loop_restarts_total{loop=\"demo\"} 2\n")
}

func TestLoopMetricsFailureDoesNotStartTheCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	parent := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))
	calls := 0
	start := func([]string) (bool, error) {
		calls++
		return true, nil
	}
	args := []string{"run", "--name", "demo", "--dir", dir, "--metrics", filepath.Join(parent, "x.prom"), "--", "true"}
	var stderr bytes.Buffer
	code, held := run(args, new(bytes.Buffer), &stderr, start)
	assert.Equal(t, 1, code)
	assert.Nil(t, held)
	assert.Equal(t, 0, calls)
	assert.Contains(t, stderr.String(), "metrics file")

	code, held = run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), start)
	require.Equal(t, 0, code, "a failed write released the lock")
	require.NotNil(t, held)
	require.NoError(t, held.Unlock())
}

func TestLoopCommandNotFoundReleasesTheLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	start := func([]string) (bool, error) { return true, exec.ErrNotFound }
	var stderr bytes.Buffer
	code, held := run(loopArgs(dir, "demo", "missing"), new(bytes.Buffer), &stderr, start)
	assert.Equal(t, 2, code)
	assert.Nil(t, held)
	assert.Contains(t, stderr.String(), "REFUSED:")

	code, held = run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	require.NoError(t, held.Unlock())
}

func TestLoopHelpAndVersionDoNotLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	start := func([]string) (bool, error) {
		t.Error("help started a command")
		return true, nil
	}
	var stdout bytes.Buffer
	code, held := run([]string{"run", "--dir", dir, "-h"}, &stdout, new(bytes.Buffer), start)
	assert.Equal(t, 0, code)
	assert.Nil(t, held)
	assert.Contains(t, stdout.String(), "nova-loop run --name")

	stdout.Reset()
	code, held = run([]string{"version"}, &stdout, new(bytes.Buffer), start)
	assert.Equal(t, 0, code)
	assert.Nil(t, held)
	assert.Contains(t, stdout.String(), "nova-loop ")

	stdout.Reset()
	code, held = run([]string{"help"}, &stdout, new(bytes.Buffer), start)
	assert.Equal(t, 0, code)
	assert.Nil(t, held)
	assert.Contains(t, stdout.String(), "nova-loop help")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "help and version write no lock")
}

func TestLoopStartErrorReleasesTheLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	start := func([]string) (bool, error) { return true, errors.New("exec failed") }
	var stderr bytes.Buffer
	code, held := run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), &stderr, start)
	assert.Equal(t, 1, code)
	assert.Nil(t, held)
	assert.Contains(t, stderr.String(), "could not be started")

	code, held = run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	require.NoError(t, held.Unlock())
}

func loopArgs(dir, name, command string) []string {
	return []string{"run", "--name", name, "--dir", dir, "--", command}
}

func okExec([]string) (bool, error) { return true, nil }

func TestLoopBusyDoesNotAdvanceTheCounter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, held := run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	require.Equal(t, 0, code)
	require.NotNil(t, held)
	t.Cleanup(func() { require.NoError(t, held.Unlock()) })

	code, second := run(loopArgs(dir, "demo", "true"), new(bytes.Buffer), new(bytes.Buffer), okExec)
	assert.Equal(t, 3, code)
	assert.Nil(t, second)
	raw, err := os.ReadFile(filepath.Join(dir, "demo.prom"))
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(raw), "} 1\n"))
	assert.False(t, strings.Contains(string(raw), "} 2\n"))
}

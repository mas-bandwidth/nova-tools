package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSwarmMirrorCoverWaitCancelled tests waitFor with an already-cancelled context.
func TestSwarmMirrorCoverWaitCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, waitFor(ctx, time.Hour))
}

// TestSwarmMirrorCoverCmdMirrorNoArgs tests cmdMirror with no arguments.
func TestSwarmMirrorCoverCmdMirrorNoArgs(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := cmdMirror([]string{}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errb.String(), "repos is required")
}

// TestSwarmMirrorCoverCmdMirrorReposButNoBase tests cmdMirror with --repos but no --base.
func TestSwarmMirrorCoverCmdMirrorReposButNoBase(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := cmdMirror([]string{"--repos", "a"}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errb.String(), "base is required")
}

// TestSwarmMirrorCoverRunMirrorDirUnderRegularFile tests runMirror with --dir set to a path beneath a regular file.
func TestSwarmMirrorCoverRunMirrorDirUnderRegularFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	dir := filepath.Join(f, "mirror")
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, nil, nil)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--dir could not be made")
}

// TestSwarmMirrorCoverRunMirrorDirWithHome sets HOME to a temp dir and tests --dir ~/m.
func TestSwarmMirrorCoverRunMirrorDirWithHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var g recordGit
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", "~/m", "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, g.run, nil)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "MIRROR OK a")
	_, err := os.Stat(filepath.Join(home, "m", "a.git"))
	assert.NoError(t, err)
}

// TestSwarmMirrorCoverRunMirrorDirWithEmptyHome tests --dir when HOME is empty.
func TestSwarmMirrorCoverRunMirrorDirWithEmptyHome(t *testing.T) {
	t.Setenv("HOME", "")
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", "~/m", "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, nil, nil)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "the user's home could not be read")
}

// TestSwarmMirrorCoverRefreshWithFile tests refresh with dir set to a regular file.
func TestSwarmMirrorCoverRefreshWithFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	var g recordGit
	m := &mirrorRun{dir: f, base: "https://example.test/o", repos: []string{"a"}, git: g.run, out: &bytes.Buffer{}}
	require.Error(t, m.refresh(context.Background(), "a"))
	assert.Empty(t, g.calls)
}

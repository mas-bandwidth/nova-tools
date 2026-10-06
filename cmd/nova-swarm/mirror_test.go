package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mirror pass uses a fake git. No test runs git, and none opens a network.

func TestMirrorPassFetchesAndClonesWithAFakeGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "a.git"), 0o755))
	var got []string
	var out bytes.Buffer
	m := &mirrorPass{
		dir: dir, remote: "git@example:{repo}.git", repos: []string{"a", "b"}, every: time.Minute,
		git: func(args ...string) error {
			got = append(got, strings.Join(args, " "))
			return nil
		},
		exists: mirrorExists,
		out:    &out,
	}
	assert.Equal(t, 0, m.run())
	assert.Contains(t, got, "-C "+filepath.Join(dir, "a.git")+" fetch --prune")
	assert.Contains(t, got, "clone --mirror git@example:b.git "+filepath.Join(dir, "b.git"))
	assert.NotContains(t, strings.Join(got, "\n"), "--shared")
	assert.Contains(t, out.String(), "MIRROR OK repos=2")
	_, err := os.Stat(filepath.Join(dir, "b.git"))
	assert.True(t, os.IsNotExist(err), "the fake git does not create the mirror")
}

func TestMirrorDryRunRunsNoGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "a.git"), 0o755))
	var out bytes.Buffer
	m := &mirrorPass{
		dir: dir, remote: "git@example:{repo}.git", repos: []string{"a", "b"}, every: time.Minute, dry: true,
		git:    func(...string) error { t.Fatal("git ran"); return nil },
		exists: mirrorExists,
		out:    &out,
	}
	assert.Equal(t, 0, m.run())
	assert.Contains(t, out.String(), "WOULD-FETCH")
	assert.Contains(t, out.String(), "WOULD-CLONE")
	_, err := os.Stat(filepath.Join(dir, "b.git"))
	assert.True(t, os.IsNotExist(err))
}

func TestMirrorRefusesHTTPSAndASlash(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--dir", t.TempDir(), "--repos", "a", "--remote", "https://example.invalid/a.git"},
		{"--dir", t.TempDir(), "--repos", "a", "--remote", "http://example.invalid/a.git"},
		{"--dir", t.TempDir(), "--repos", "a/b"},
		{"--dir", t.TempDir(), "--repos", ".."},
		{"--repos", "a"},
		{"--dir", t.TempDir(), "--repos", "a", "--every", "0s"},
	} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 2, cmdMirror(args, &stdout, &stderr), "%v\n%s", args, stderr.String())
		assert.Empty(t, stdout.String(), "%v", args)
		assert.Contains(t, stderr.String(), "nova-swarm mirror:", "%v", args)
	}
}

func TestMirrorHelpNamesTheFlags(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "mirror", "-h")
	for _, flag := range []string{"--dir", "--repos", "--remote", "--every", "--dry-run"} {
		assert.Contains(t, help, "\n  "+flag+" ", flag)
	}
	assert.Contains(t, help, "effect: local write:")
	assert.Equal(t, help, swarmHelp(t, "help", "mirror"))
}

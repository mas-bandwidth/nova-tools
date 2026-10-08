package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFolderInstallCarriesRealHarnessSessionAndRoute(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	target := t.TempDir()
	cli := r.cli()
	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--session", "real-thread", "--adapter", "folder", "--delivery-dir", target, "--dry-run").Exit(0).
		Out("--harness codex", "--session real-thread", "--adapter folder", "--delivery-dir "+target)
	cli.Do(t, "check", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--session", "real-thread", "--adapter", "folder", "--delivery-dir", target, "--dry-run").Exit(0)
	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--adapter", "folder", "--delivery-dir", target, "--dry-run").Exit(2).Err("--session <real session>")
	cli.Do(t, "check", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--session", "real-thread", "--adapter", "folder", "--delivery-dir", filepath.Join(target, "missing"), "--dry-run").Exit(2).Err("not an existing directory")
	assert.NoFileExists(t, filepath.Join(target, "pong.json"))
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries, "dry run and refused flags never deliver")
	assert.NotContains(t, strings.Join(r.launchctl, "\n"), "bootstrap")
}

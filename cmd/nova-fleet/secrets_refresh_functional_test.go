//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSecretsRefreshSymlinkScanFollowsOnlyTheStoreRoot runs the role's own scan of the control node's clone, its argv
// rendered for a store reached through a symlink (the default path linked to the clone): the link at the root is
// followed and reports nothing, and a link inside the store is still reported (and refused by the next assert).
func TestSecretsRefreshSymlinkScanFollowsOnlyTheStoreRoot(t *testing.T) {
	t.Parallel()
	var argv []string
	for _, task := range secretsRefreshTasks(t) {
		if task["register"] == "sr_links" {
			for _, a := range task["ansible.builtin.command"].(map[string]any)["argv"].([]any) {
				argv = append(argv, asText(a))
			}
		}
	}
	require.NotEmpty(t, argv, "no task registers sr_links")
	scan := func(store string) string {
		args := make([]string, len(argv))
		for i, a := range argv {
			a = strings.ReplaceAll(a, "{{ nova_seal_store | regex_replace('/*$', '') }}", store)
			args[i] = strings.ReplaceAll(a, "{{ nova_seal_store }}", store)
		}
		out, err := exec.Command(args[0], args[1:]...).Output()
		require.NoError(t, err, "%v", args)
		return strings.TrimSpace(string(out))
	}
	dir := t.TempDir()
	clone := filepath.Join(dir, "clone")
	require.NoError(t, os.MkdirAll(filepath.Join(clone, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(clone, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	require.NoError(t, os.Symlink("../HEAD", filepath.Join(clone, ".git", "head-link")))
	store := filepath.Join(dir, "store")
	require.NoError(t, os.Symlink(clone, store))
	assert.Empty(t, scan(store), "a store root reached through a symlink, with no link inside, holds no symlink")
	require.NoError(t, os.Symlink(".sops.yaml", filepath.Join(clone, "inside")))
	assert.Equal(t, filepath.Join(store, "inside"), scan(store), "a link inside the store is still reported")
}

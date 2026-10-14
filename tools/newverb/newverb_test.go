package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVerbScaffoldValidatesInput(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	_, err := verb(tree, "nova-ci", "sample")
	require.Error(t, err, "a tree with no go.mod")

	require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "cmd", "nova-ci"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "cmd", "nova-ci", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))

	for _, bad := range []string{"123num", "BadName", "rule with spaces", "for", "type"} {
		_, err := verb(tree, "nova-ci", bad)
		assert.Error(t, err, "invalid verb name %q", bad)
		_, err = verb(tree, bad, "probe")
		assert.Error(t, err, "invalid tool name %q", bad)
	}

	written, err := verb(tree, "nova-ci", "my-verb")
	require.NoError(t, err, "a valid name")
	assert.Len(t, written, 4)

	_, err = verb(tree, "nova-ci", "my-verb")
	assert.Error(t, err, "a duplicate scaffold")
}

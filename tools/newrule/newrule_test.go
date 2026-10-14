package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRuleScaffoldValidatesInput(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	_, err := rule(tree, "sample")
	require.Error(t, err, "a tree with no go.mod")

	require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644))

	for _, bad := range []string{"123num", "BadName", "rule with spaces", "for", "type"} {
		_, err := rule(tree, bad)
		assert.Error(t, err, "invalid rule name %q", bad)
	}

	written, err := rule(tree, "my-rule")
	require.NoError(t, err, "a valid name")
	assert.Len(t, written, 3)

	_, err = rule(tree, "my-rule")
	assert.Error(t, err, "a duplicate scaffold")
}

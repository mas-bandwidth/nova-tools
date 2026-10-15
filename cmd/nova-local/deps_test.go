package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaLocalShellsOutForNothing pins rule 1 of docs/SPEC-LOCAL.md: this binary learns
// the box's memory and load in process, so none of its non-test files may import os/exec,
// pkg/subproc (which runs top and lsof) or pkg/hostload (which reaches pkg/subproc). It
// reads import lists only and runs no go command.
func TestNovaLocalShellsOutForNothing(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err, name)
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err, name)
			bad := path == "os/exec" || strings.HasSuffix(path, "/pkg/subproc") || strings.HasSuffix(path, "/pkg/hostload")
			assert.False(t, bad, "%s imports %s: rule 1 forbids shelling out", name, path)
		}
	}
	assert.Greater(t, checked, 0, "no non-test files checked")
}

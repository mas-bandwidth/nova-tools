//go:build functional

package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestATransitiveChangeRebuildsTheTool asks the real go list about a chain
// cmd/nova-a -> internal/b -> internal/c, where nova-a never imports c
// itself: a change under c rebuilds nova-a, because go list's .Deps is every
// package a tool imports recursively, and an untouched tool beside it is
// reused.
func TestATransitiveChangeRebuildsTheTool(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for path, body := range map[string]string{
		"go.mod":                "module example.com/m\n\ngo 1.22\n",
		"cmd/nova-a/main.go":    "package main\n\nimport \"example.com/m/internal/b\"\n\nfunc main() { b.B() }\n",
		"cmd/nova-z/main.go":    "package main\n\nfunc main() {}\n",
		"internal/b/b.go":       "package b\n\nimport \"example.com/m/internal/c\"\n\nfunc B() { c.C() }\n",
		"internal/c/c.go":       "package c\n\nfunc C() {}\n",
		"internal/c/sql/one.sq": "-- an embedded-style file below c\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(body), 0o644))
	}
	dirs, err := ExecSource{}.Packages(context.Background(), root, "linux", "amd64", []string{"./cmd/nova-a", "./cmd/nova-z"})
	require.NoError(t, err)
	assert.Equal(t, []string{"cmd/nova-a", "internal/b", "internal/c"}, dirs["./cmd/nova-a"])

	byTool := map[string][]string{"nova-a": dirs["./cmd/nova-a"], "nova-z": dirs["./cmd/nova-z"]}
	all := func(string) bool { return true }
	for _, changed := range []string{"internal/c/c.go", "internal/c/sql/one.sq"} {
		rebuild, reuse, why := rebuildSet([]string{"nova-a", "nova-z"}, byTool, []string{changed}, all)
		assert.Equal(t, []string{"nova-a"}, rebuild, changed)
		assert.Equal(t, []string{"nova-z"}, reuse, changed)
		assert.Equal(t, changed, why["nova-a"])
	}
}

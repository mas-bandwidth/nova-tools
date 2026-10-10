package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// batchPackages folds a batch's changed files through a `go list -deps`-shaped
// graph: the package each changed .go file stands in and every package that
// imports one of them, transitively, and nothing else (docs/SPEC-SPRINT.md, the
// tree gate). The graph is a fake, so the test pins the rule, not the toolchain.
func TestBatchPackagesNamesTheTouchedAndTheirImporters(t *testing.T) {
	t.Parallel()
	deps := map[string][]string{
		"example.com/m/a": {"fmt"},
		"example.com/m/b": {"example.com/m/a"},
		"example.com/m/c": {"example.com/m/a", "example.com/m/b"},
		"example.com/m/d": {"fmt"},
	}
	assert.Equal(t, []string{"example.com/m/a", "example.com/m/b", "example.com/m/c"},
		batchPackages([]string{"a/a.go"}, deps), "the touched package and every package that imports it, transitively")
	assert.Equal(t, []string{"example.com/m/d"}, batchPackages([]string{"d/d.go"}, deps),
		"a package nothing imports is tested alone")
	assert.Empty(t, batchPackages([]string{"a/notes.txt"}, deps),
		"a change no .go file stands in tests no package")
	assert.Empty(t, batchPackages(nil, deps))
}

// A batch that breaks a package it does not touch directly is refused by the
// tree gate, which tests the packages the batch's changed files touch and every
// package that imports them; the finding names the broken package. This is the
// regression of the 2026-10-04 batches that broke cmd/nova-bus, cmd/nova-cairn,
// cmd/nova-memory and internal/update and landed on a gate that tested only
// internal/docs and internal/ci (docs/SPEC-SPRINT.md, the tree gate).
func TestTheTreeGateRefusesABatchThatBreaksAnotherPackage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n",
		"a/a.go": "package a\n\nfunc Answer() int { return 2 }\n",
		"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\nfunc Use() int { return a.Answer() }\n",
		"b/b_test.go": "package b\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\n" +
			"func TestAnswer(t *testing.T) {\n\tif got := a.Answer(); got != 1 {\n\t\tt.Fatalf(\"Answer() = %d, want 1\", got)\n\t}\n}\n",
		"c/c.go":      "package c\n\nfunc C() {}\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) { t.Fatal(\"c is not of this batch\") }\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	// the test process may run under GOFLAGS=-json (make test): the gate must see a plain
	// go test's output, so the lander's environment drops the flags that reshape it.
	ta := newTestApp(t)
	ta.a.gitEnv = slices.DeleteFunc(os.Environ(), func(e string) bool {
		name, _, _ := strings.Cut(e, "=")
		return name == "GOFLAGS" || strings.HasPrefix(name, "GOTEST")
	})
	l := &lander{a: ta.a}
	// the batch touches only a/a.go; b imports a and its test breaks, c neither imports a
	// nor is touched
	why := l.treeGate(t.Context(), dir, true, "a/a.go")
	require.NotEmpty(t, why, "a batch that breaks an importer is refused")
	assert.Contains(t, why, "example.com/m/b", "the finding names the package the batch breaks")
	assert.NotContains(t, why, "example.com/m/c", "a package the batch does not touch and that does not import it is not tested")
}

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The derived tag strips the registry and the :tag, so a shared name derives one tag (rule 6).
func TestDerivedTag(t *testing.T) {
	t.Parallel()
	for ref, want := range map[string]string{
		"gemma4:12b": "gemma4-32k", "gemma4": "gemma4-32k", "registry.example/lib/gemma4:27b": "gemma4-32k", "m:7b": "m-32k",
	} {
		assert.Equal(t, want, DerivedTag(ref, 32768), ref)
	}
	assert.Empty(t, ContextProblem(32768))
	assert.Contains(t, ContextProblem(30000), "use 29696 or 30720")
	assert.Contains(t, ContextProblem(0), "refusing to guess")
}

// A store is shared exactly under the shared root, a whole component at a time, and a
// path is resolved through its longest existing prefix (rule 15).
func TestSharedAndResolve(t *testing.T) {
	t.Parallel()
	root := SharedRoot("/ai")
	assert.Equal(t, "/ai/shared/models", root)
	for store, want := range map[string]string{
		"/ai/shared/models": "yes", "/ai/shared/models/ollama": "yes", "/ai/shared/models-scratch/ollama": "no", "/home/x/.ollama/models": "no", "/ai/shared": "no",
	} {
		got, _ := Shared(store, root)
		assert.Equal(t, want, got, store)
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	require.NoError(t, os.Mkdir(real, 0o755))
	require.NoError(t, os.Symlink(real, filepath.Join(dir, "link")))
	resolvedReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	got, err := Resolve(filepath.Join(dir, "link", "missing", "blobs"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolvedReal, "missing", "blobs"), got)
	got, err = Resolve("/ai-nowhere/shared")
	require.NoError(t, err)
	assert.Equal(t, "/ai-nowhere/shared", got)
	assert.Equal(t, "/r", AIRoot(func(string) string { return "/r" }, "/h"))
	assert.Equal(t, "/h/ai", AIRoot(func(string) string { return "" }, "/h"))
	assert.Empty(t, AIRoot(func(string) string { return "" }, ""))
}

// The serving-host rule: loopback or the tailnet, a name by every address it resolves to (rule 3).
func TestTheServingHost(t *testing.T) {
	t.Parallel()
	lookup := func(h string) ([]string, error) {
		switch h {
		case "tail":
			return []string{"100.64.0.1", "fd7a:115c:a1e0::1"}, nil
		case "mixed":
			return []string{"100.64.0.1", "8.8.8.8"}, nil
		}
		return nil, errors.New("no such host")
	}
	for host, ok := range map[string]bool{
		"127.0.0.1": true, "::1": true, "localhost": true, "100.127.255.254": true, "100.128.0.1": false, "10.0.0.5": false,
		"tail": false, "mixed": false, "nowhere": false,
	} {
		assert.Equal(t, ok, ServingHost(host, lookup) == "", host)
	}
	assert.Contains(t, BaseProblem("ftp://127.0.0.1/v1", lookup), "wants an http URL")
	assert.Empty(t, BaseProblem("http://127.0.0.1:11434/v1", lookup))
}

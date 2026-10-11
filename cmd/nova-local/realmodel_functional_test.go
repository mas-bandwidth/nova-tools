//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tinyModel is the smallest model an ollama daemon on this machine's loopback advertises
// from the shared store, or why there is none to run: no AI root, no shared ollama store,
// no daemon answering, or no model in it.
func tinyModel(t *testing.T, w world) (string, string) {
	home, _ := os.UserHomeDir() // ignored: no home is no AI root, and the test is skipped
	root := AIRoot(os.Getenv, home)
	if root == "" {
		return "", "no AI root: set " + AIRootEnv
	}
	store := filepath.Join(SharedRoot(root), "ollama")
	if fi, err := os.Stat(store); err != nil || !fi.IsDir() {
		return "", "no shared ollama store at " + store
	}
	o := Ollama{}
	c := Client{Base: o.DefaultBase(), Do: w.do}
	ms, err := o.Models(context.Background(), c)
	if err != nil || len(ms) == 0 {
		return "", "no ollama daemon with a model answers on loopback"
	}
	slices.SortFunc(ms, func(a, b Model) int { return int(a.Weights - b.Weights) })
	return ms[0].Ref, ""
}

// A real tiny model, served from the shared store by a real ollama on loopback: status
// reads it shared, serve makes its derived tag and loads it, and --stop unloads it. It
// runs only where such a model is present, and is skipped elsewhere.
func TestARealTinyModelIsServedFromTheSharedStore(t *testing.T) {
	t.Parallel()
	w := realWorld()
	model, why := tinyModel(t, w)
	if model == "" {
		t.Skip(why)
	}
	run := testkit.Main(localTool(w).Run)
	r := run.OK(t, "status", "--engine", "ollama")
	assert.Contains(t, r.Stdout, "state=up")
	assert.Contains(t, r.Stdout, "shared=yes")
	r = run.OK(t, "serve", "--engine", "ollama", "--model", model, "--num-ctx", "2048")
	require.Contains(t, r.Stdout, "SERVE OK ")
	tag := DerivedTag(model, 2048)
	assert.Contains(t, r.Stdout, "serve_as="+tag+" ")
	assert.True(t, strings.Contains(r.Stdout, "load="), r.Stdout)
	r = run.OK(t, "serve", "--stop", "--engine", "ollama", "--model", tag)
	assert.Contains(t, r.Stdout, "stopped=yes")
}

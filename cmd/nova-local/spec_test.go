package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spec's three verbs (docs/SPEC-LOCAL.md, "The verbs"), each run against a fake
// ollama through the injected transport: no model, no socket, no real time. status
// reports the engine and the box; serve makes the derived tag, warms it on the fake
// clock and stops it, and its --dry-run creates and loads nothing; worker writes the one
// description nova-swarm accepts, and its --dry-run writes nothing.
func TestTheThreeVerbsOfTheSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := filepath.Join(dir, "local.key")
	require.NoError(t, os.WriteFile(key, []byte("local\n"), 0o600))
	home := filepath.Join(dir, "home")
	require.NoError(t, os.Mkdir(home, 0o755))
	out := filepath.Join(dir, "gemma.json")
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")
	worker := []string{"worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", out, "--name", "gemma",
		"--harness", "opencode", "--harness-args", "run,--model,ollama/{model},--,{prompt}", "--worker-dir", home,
		"--key-file", key, "--env-var", "OLLAMA_API_KEY", "--usage", "opencode", "--deadline", "20m"}

	t.Run("status", func(t *testing.T) {
		r := run.OK(t, "status", "--list")
		assert.Contains(t, r.Stdout, "STATUS OK engines=1 answering=1 loaded=0 models=1 ")
		assert.Contains(t, r.Stdout, "STATUS ENGINE name=ollama state=up base=http://127.0.0.1:11434/v1 ")
		assert.Contains(t, r.Stdout, "STATUS MODEL engine=ollama model=gemma4:12b digest=sha256:c0e0c3e5b4a1 weights=8149190253 loaded=no")
	})

	t.Run("serve --dry-run creates and loads nothing", func(t *testing.T) {
		r := run.OK(t, append(args(serveGemma), "--dry-run")...)
		assert.Contains(t, r.Stdout, "serve_as=gemma4-32k ")
		assert.Empty(t, f.creates)
		assert.Empty(t, f.generates)
	})

	t.Run("worker for a tag not yet served is exit 1 naming serve", func(t *testing.T) {
		r := run.Run(worker...)
		assert.Equal(t, 1, r.Code, r.Stderr)
		assert.Contains(t, r.Stderr, "run: nova-local serve")
		assert.NoFileExists(t, out)
	})

	t.Run("serve", func(t *testing.T) {
		r := run.OK(t, args(serveGemma)...)
		assert.Contains(t, r.Stdout, "SERVE OK engine=ollama model=gemma4:12b serve_as=gemma4-32k digest=sha256:c0e0c3e5b4a1 num_ctx=32768 keep_alive=30m temperature=0 seed=7 created=yes load=3s ")
		assert.Contains(t, r.Stdout, " store=/ai/shared/models/ollama shared=yes")
		assert.Len(t, f.creates, 1)
		assert.Len(t, f.generates, 1)
	})

	t.Run("worker --dry-run writes nothing", func(t *testing.T) {
		r := run.OK(t, append(worker, "--dry-run")...)
		assert.Contains(t, r.Stdout, "WORKER OK engine=ollama model=gemma4-32k out="+out+" workers=1 ")
		assert.NoFileExists(t, out)
	})

	t.Run("worker", func(t *testing.T) {
		r := run.OK(t, worker...)
		assert.Contains(t, r.Stdout, "WORKER OK engine=ollama model=gemma4-32k out="+out+" workers=1 provider=ollama harness=opencode deadline=20m base=http://127.0.0.1:11434/v1")
		w, problems := swarm.LoadWorker(out)
		assert.Empty(t, problems)
		assert.Equal(t, "gemma4-32k", w.Model)
	})

	t.Run("serve --stop", func(t *testing.T) {
		r := run.OK(t, "serve", "--stop", "--engine", "ollama", "--model", "gemma4-32k")
		assert.Contains(t, r.Stdout, "SERVE OK stopped=yes engine=ollama model=gemma4-32k\n")
		assert.Len(t, f.generates, 2)
	})
}

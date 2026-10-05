package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spec's three verbs (docs/SPEC-LOCAL.md, "The verbs"), each run once against the fake
// engine, in the order a first run types them: status reports and writes nothing, serve
// --dry-run reads everything and creates and loads nothing, serve makes and warms the
// derived tag, worker --dry-run writes nothing, worker writes the one description
// nova-swarm accepts, and serve --stop unloads the tag and keeps it on disk. No model, no network, no real time.
func TestTheThreeVerbsOfTheSpec(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")

	r := run.OK(t, "status")
	assert.Contains(t, r.Stdout, "STATUS OK engines=1 answering=1 loaded=0 models=1 ")
	assert.Contains(t, r.Stdout, "STATUS ENGINE name=ollama state=up base=http://127.0.0.1:11434/v1 ")

	r = run.OK(t, append(args(serveGemma), "--dry-run")...)
	assert.Contains(t, r.Stdout, "serve_as=gemma4-32k ")
	assert.Empty(t, f.creates, "--dry-run creates nothing")
	assert.Empty(t, f.generates, "--dry-run loads nothing")

	r = run.OK(t, args(serveGemma)...)
	assert.Contains(t, r.Stdout, "SERVE OK engine=ollama model=gemma4:12b serve_as=gemma4-32k digest=sha256:c0e0c3e5b4a1 num_ctx=32768 keep_alive=30m temperature=0 seed=7 created=yes load=3s ")
	assert.Len(t, f.creates, 1)
	assert.Len(t, f.generates, 1)

	dir, home := t.TempDir(), t.TempDir()
	key := filepath.Join(dir, "local.key")
	require.NoError(t, os.WriteFile(key, []byte("local\n"), 0o600))
	out := filepath.Join(dir, "gemma.json")
	worker := []string{"worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", out, "--name", "gemma", "--harness", "opencode",
		"--harness-args", "run,--model,ollama/{model},--,{prompt}", "--worker-dir", home, "--key-file", key,
		"--env-var", "OLLAMA_API_KEY", "--usage", "opencode", "--deadline", "20m"}
	r = run.OK(t, append(worker, "--dry-run")...)
	assert.Contains(t, r.Stdout, "written=no")
	assert.NoFileExists(t, out, "--dry-run writes nothing")
	r = run.OK(t, worker...)
	assert.Contains(t, r.Stdout, "WORKER OK engine=ollama model=gemma4-32k out="+out+" workers=1 ")
	_, problems := swarm.LoadWorker(out)
	assert.Empty(t, problems)

	r = run.OK(t, "serve", "--stop", "--engine", "ollama", "--model", "gemma4-32k")
	assert.Contains(t, r.Stdout, "SERVE OK stopped=yes engine=ollama model=gemma4-32k\n")
	assert.True(t, f.has("gemma4-32k:latest"), "the tag stays on disk")
}

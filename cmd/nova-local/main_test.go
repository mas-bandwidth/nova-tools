package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const serveGemma = "serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7"

func args(s string) []string { return strings.Fields(s) }

// status reads the engine and the box at the moment of the call (rules 5, 10, 12, 15):
// digest and weights as the engine reported them, num_ctx only for a loaded model, the
// store resolved and judged against the AI root's shared/models, and no fits= or load=.
func TestStatusPrintsTheEngineAndTheBox(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")
	r := run.OK(t, "status", "--list")
	assert.Contains(t, r.Stdout, "STATUS OK engines=1 answering=1 loaded=0 models=1 mem_used=71940702208 mem_free=65498251264 mem_total=137438953472 wired_cap=unset load1=14.20")
	assert.Contains(t, r.Stdout, "STATUS ENGINE name=ollama state=up base=http://127.0.0.1:11434/v1 loaded=0 advertised=1 store=/ai/shared/models/ollama shared=yes")
	assert.Contains(t, r.Stdout, "STATUS MODEL engine=ollama model=gemma4:12b digest=sha256:c0e0c3e5b4a1 weights=8149190253 loaded=no\n")
	for _, never := range []string{"fits=", "load=", "gen="} {
		assert.NotContains(t, r.Stdout, never)
	}
	run.OK(t, args(serveGemma)...)
	r = run.OK(t, "status", "--list")
	assert.Contains(t, r.Stdout, "loaded=1 advertised=2 num_ctx=32768")
	assert.Contains(t, r.Stdout, "STATUS MODEL engine=ollama model=gemma4-32k:latest digest=sha256:9a8b7c6d5e4f weights=8149190253 loaded=yes num_ctx=32768")
}

// The memory is read at the moment of each call, never cached (rule 10).
func TestStatusReadsTheMemoryEachCall(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	free := uint64(61 << 30)
	w := world{do: f.do, box: fakeBox(), now: f.now, lookup: fakeLookup, getenv: func(string) string { return "/ai" }, stat: os.Stat, adapters: Adapters()}
	w.box.Mem = func() (uint64, uint64, bool) { free -= 1 << 30; return free, 128 << 30, true }
	run := testkit.Main(localTool(w).Run)
	a, b := run.OK(t, "status").Stdout, run.OK(t, "status").Stdout
	assert.Contains(t, a, fmt.Sprintf("mem_free=%d", uint64(60<<30)))
	assert.Contains(t, b, fmt.Sprintf("mem_free=%d", uint64(59<<30)))
}

// With no engine answering, status is one remedy line at exit 1, never the state (rule 13).
func TestStatusWithNothingAnsweringIsOneRemedyLine(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	f.down = true
	r := rig(f, "/ai").Run("status")
	assert.Equal(t, 1, r.Code)
	assert.Equal(t, "STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve\n", r.Stderr)
	assert.Empty(t, r.Stdout)
}

// 400 advertised models print at most --max lines and one MORE, under 4,096 bytes (rule 13).
func TestStatusListingIsBounded(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	for i := range 399 {
		f.tags = append(f.tags, fakeTag{Name: fmt.Sprintf("m%03d:7b", i), Size: 1, Digest: "ab"})
	}
	r := rig(f, "/ai").OK(t, "status", "--list")
	assert.Equal(t, 20, strings.Count(r.Stdout, "STATUS MODEL "))
	assert.Contains(t, r.Stdout, "STATUS MORE kind=model shown=20 total=400")
	assert.Less(t, len(r.Stdout), 4096)
	assert.NotContains(t, rig(f, "/ai").OK(t, "status").Stdout, "STATUS MODEL", "the counts alone without --list")
}

// --base is loopback or the tailnet's, on every verb; a name must resolve there too (rule 3).
func TestBaseIsLoopbackOrTheTailnet(t *testing.T) {
	t.Parallel()
	run := rig(newFake("/ai/shared/models/ollama"), "/ai")
	for _, base := range []string{"http://elsewhere.test:11434/v1", "http://nowhere.test:11434/v1"} {
		for _, verb := range [][]string{{"status", "--engine", "ollama"}, args(serveGemma), {"worker", "--engine", "ollama"}} {
			r := run.Run(append(verb, "--base", base)...)
			assert.Equal(t, 2, r.Code, "%v --base %s", verb, base)
			assert.Contains(t, r.Stderr, "--base", "%v --base %s", verb, base)
		}
	}
	for _, base := range []string{"http://127.0.0.1:9999/v1", "http://[::1]:9999/v1", "http://gpu-box.test:11434/v1"} {
		r := run.OK(t, "status", "--engine", "ollama", "--base", base)
		assert.Contains(t, r.Stdout, "base="+base)
	}
	assert.Contains(t, run.OK(t, "status").Stdout, "base=http://127.0.0.1:11434/v1", "the default is printed")
}

// serve has no default context, and a context that is no multiple of 1024 names the two
// nearest (rules 5 and 6).
func TestServeRefusesToGuessTheContext(t *testing.T) {
	t.Parallel()
	run := rig(newFake("/ai/shared/models/ollama"), "/ai")
	r := run.Run("serve", "--engine", "ollama", "--model", "gemma4:12b")
	assert.Equal(t, 2, r.Code)
	assert.Contains(t, r.Stderr, "refusing to guess")
	r = run.Run("serve", "--engine", "ollama", "--model", "gemma4:12b", "--num-ctx", "30000")
	assert.Equal(t, 2, r.Code)
	assert.Contains(t, r.Stderr, "29696")
	assert.Contains(t, r.Stderr, "30720")
}

// serve creates <name>-<ctx>k with num_ctx, temperature 0 and the seed baked in, uses it
// as it stands when the four compared things match (whatever else the engine reports),
// and refuses one that differs, naming both and ollama rm (rule 6).
func TestServeMakesTheDerivedTagOnce(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	f.tags = append(f.tags, fakeTag{Name: "registry.example/lib/gemma4:27b", Size: 2, Digest: "ff"})
	run := rig(f, "/ai")
	r := run.OK(t, args(serveGemma)...)
	assert.Contains(t, r.Stdout, "serve_as=gemma4-32k ")
	assert.Contains(t, r.Stdout, "created=yes")
	require.Len(t, f.creates, 1)
	assert.Equal(t, map[string]any{"num_ctx": 32768.0, "temperature": 0.0, "seed": 7.0}, f.creates[0]["parameters"])
	assert.Equal(t, "gemma4:12b", f.creates[0]["from"])

	r = run.OK(t, args(serveGemma)...)
	assert.Contains(t, r.Stdout, "created=no", "the engine reports top_k and top_p the tool never set")
	assert.Len(t, f.creates, 1)

	r = run.Run(args("serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 8")...)
	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Stderr, "seed 7, and this serve asks 8; run: ollama rm gemma4-32k")

	r = run.Run(args("serve --engine ollama --model registry.example/lib/gemma4:27b --num-ctx 32768 --seed 7")...)
	assert.Equal(t, 1, r.Code, "a shared name derives one tag, and the parent catches the collision")
	assert.Contains(t, r.Stderr, "parent gemma4:12b, and this serve asks registry.example/lib/gemma4:27b")
	assert.Len(t, f.creates, 1)

	r = run.Run(args("serve --engine ollama --model llama9:1b --num-ctx 4096")...)
	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Stderr, "run: ollama pull llama9:1b")
}

// Exactly one warm-up carries keep_alive, and load= is the clock's advance across it;
// --stop sends keep_alive 0 and keeps the tag; a second --stop is already stopped (rule 7).
func TestServeTimesOneWarmUpAndStops(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")
	r := run.OK(t, args(serveGemma)...)
	assert.Contains(t, r.Stdout, "keep_alive=30m")
	assert.Contains(t, r.Stdout, "load=3s")
	require.Len(t, f.generates, 1)
	assert.Equal(t, "30m", f.generates[0]["keep_alive"])

	r = run.OK(t, "serve", "--stop", "--engine", "ollama", "--model", "gemma4-32k")
	assert.Contains(t, r.Stdout, "SERVE OK stopped=yes engine=ollama model=gemma4-32k\n")
	require.Len(t, f.generates, 2)
	assert.Equal(t, 0.0, f.generates[1]["keep_alive"])
	assert.True(t, f.has("gemma4-32k:latest"), "the tag stays on disk")

	r = run.OK(t, "serve", "--stop", "--engine", "ollama", "--model", "gemma4-32k")
	assert.Contains(t, r.Stdout, "already stopped")
	assert.Len(t, f.generates, 2)
}

// The box is read before anything starts and printed; it refuses only past a value the
// caller gave (rule 8). A digest differing from --expect-digest serves nothing (rule 4).
func TestServeRefusesOnlyPastTheCallersValues(t *testing.T) {
	t.Parallel()
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")
	r := run.Run(append(args(serveGemma), "--max-load", "8.0")...)
	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Stderr, "load1 is 14.20 and --max-load asks at most 8.0")
	assert.Contains(t, r.Stderr, "load1=14.20")
	r = run.Run(append(args(serveGemma), "--expect-digest", "sha256:0000")...)
	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Stderr, "gemma4:12b has digest sha256:c0e0c3e5b4a1 and --expect-digest asks sha256:0000; run: nova-local status --engine ollama --list")
	assert.Empty(t, f.creates, "nothing was served")
	r = run.OK(t, append(args(serveGemma), "--max-load", "20.0", "--min-free", "16G", "--expect-digest", "sha256:c0e0c3e5b4a1")...)
	assert.Contains(t, r.Stdout, "mem_free=65498251264")
}

// store= is the resolved blobs parent; shared=yes exactly under the AI root's
// shared/models, matched a component at a time, through a symlink too; the flag refuses
// shared=no and nothing else, and status never refuses (rule 15, "Fleet").
func TestTheSharedStore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "models")
	require.NoError(t, os.MkdirAll(filepath.Join(shared, "ollama"), 0o755))
	link := filepath.Join(root, "home-models")
	require.NoError(t, os.Symlink(filepath.Join(shared, "ollama"), link))
	resolvedShared, err := Resolve(shared)
	require.NoError(t, err)
	for _, c := range []struct {
		name, store, shared string
		code                int
	}{
		{"under the shared root", filepath.Join(shared, "ollama"), "shared=yes", 0},
		{"through a symlink into it", link, "shared=yes", 0},
		{"its neighbour", filepath.Join(root, "shared", "models-scratch", "ollama"), "shared=no", 1},
		{"an account's own", filepath.Join(root, "rowan-working", "ollama"), "shared=no", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			run := rig(newFake(c.store), root)
			assert.Contains(t, run.OK(t, "status").Stdout, " "+c.shared)
			r := run.Run(append(args(serveGemma), "--require-shared-store")...)
			assert.Equal(t, c.code, r.Code, r.Stderr)
			if c.code == 1 {
				assert.Contains(t, r.Stderr, "not under the shared model directory "+resolvedShared)
				assert.Equal(t, 0, run.Run(args(serveGemma)...).Code, "without the flag it serves and prints shared=no")
			}
		})
	}
	r := rig(newFake(filepath.Join(shared, "ollama")), "").OK(t, "status")
	assert.Contains(t, r.Stdout, "shared=unknown: no AI root")
}

// worker writes nova-worker's description: it decodes with zero problems, carries
// workers=1 and no temperature, seed, num_ctx or max_workers, and the key file is
// stat'ed, never opened (rules 9, 11, 14; tests 10, 14).
func TestWorkerWritesADescriptionNovaSwarmAccepts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := filepath.Join(dir, "local.key")
	require.NoError(t, os.WriteFile(key, []byte("local\n"), 0o600))
	require.NoError(t, os.Chmod(key, 0)) // never opened, so mode 0000 still serves
	home := filepath.Join(dir, "home")
	require.NoError(t, os.Mkdir(home, 0o755))
	out := filepath.Join(dir, "gemma.json")
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")
	run.OK(t, args(serveGemma)...)
	before := entries(t, dir)
	r := run.OK(t, "worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", out, "--name", "gemma", "--harness", "opencode",
		"--harness-args", "run,--model,ollama/{model},--,{prompt}", "--worker-dir", home, "--key-file", key,
		"--env-var", "OLLAMA_API_KEY", "--usage", "opencode", "--deadline", "20m")
	assert.Contains(t, r.Stdout, "WORKER OK engine=ollama model=gemma4-32k out="+out+" workers=1 provider=ollama harness=opencode deadline=20m base=http://127.0.0.1:11434/v1")
	assert.ElementsMatch(t, append(before, "gemma.json"), entries(t, dir), "one file written, the --out path")
	require.NoError(t, os.Chmod(key, 0o600))
	w, problems := swarm.LoadWorker(out)
	assert.Empty(t, problems)
	assert.Equal(t, "gemma4-32k", w.Model)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var keys map[string]any
	require.NoError(t, json.Unmarshal(raw, &keys))
	for _, never := range []string{"temperature", "seed", "num_ctx", "max_workers"} {
		assert.NotContains(t, keys, never)
	}
	assert.NotContains(t, string(raw)+r.Stdout+r.Stderr, "local\n")
}

func entries(t *testing.T, dir string) []string {
	des, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	return out
}

// worker names every problem at once, each exit 2 with the field; a model the engine does
// not serve is exit 1 naming serve (test 15).
func TestWorkerRefusesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.key")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	run := rig(newFake("/ai/shared/models/ollama"), "/ai")
	r := run.Run("worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", filepath.Join(dir, "w.json"), "--name", "g", "--harness", "opencode",
		"--harness-args", "run,--", "--worker-dir", "home", "--key-file", empty, "--env-var", "X", "--usage", "sometimes", "--deadline", "soon")
	assert.Equal(t, 2, r.Code)
	for _, want := range []string{"{model}", "--worker-dir wants an absolute directory", "printf 'local\\n' >", "--usage wants opencode", "--deadline wants a Go duration"} {
		assert.Contains(t, r.Stderr, want)
	}
	r = run.Run("worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", filepath.Join(dir, "w.json"), "--name", "g", "--harness", "opencode",
		"--harness-args", "run,--model,ollama/{model}", "--worker-dir", dir, "--key-file", filepath.Join(dir, "absent.key"), "--env-var", "X", "--usage", "none", "--deadline", "1m")
	assert.Equal(t, 2, r.Code)
	assert.Contains(t, r.Stderr, "absent.key does not exist")
	require.NoError(t, os.WriteFile(empty, []byte("local\n"), 0o600))
	r = run.Run("worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", filepath.Join(dir, "w.json"), "--name", "g", "--harness", "opencode",
		"--harness-args", "run,--model,ollama/{model}", "--worker-dir", dir, "--key-file", empty, "--env-var", "X", "--usage", "none", "--deadline", "1m")
	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Stderr, "does not serve gemma4-32k")
	assert.Contains(t, r.Stderr, "run: nova-local serve --engine ollama")
	assert.NoFileExists(t, filepath.Join(dir, "w.json"))
}

// Three verbs and the version: any other first word is exit 2 naming the door (rule 11).
func TestOnlyThreeVerbs(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"pull", "eval", "trust", "ask"} {
		r := cli.Run(v)
		assert.Equal(t, 2, r.Code, v)
		assert.Contains(t, r.Stderr, "the verbs are", v)
	}
	assert.Empty(t, localTool(world{}).Problems())
}

// The card's test: each of the spec's three verbs runs against the fake engine (no real
// model, no network, no real time) and prints its line from docs/SPEC-LOCAL.md, "The
// verbs"; serve's and worker's --dry-run read everything and create, load and write nothing.
func TestTheThreeVerbsOfTheSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := filepath.Join(dir, "local.key")
	require.NoError(t, os.WriteFile(key, []byte("local\n"), 0o600))
	f := newFake("/ai/shared/models/ollama")
	run := rig(f, "/ai")

	r := run.OK(t, "status")
	assert.Contains(t, r.Stdout, "STATUS OK engines=1 answering=1 loaded=0 models=1 ")
	assert.Contains(t, r.Stdout, "STATUS ENGINE name=ollama state=up ")

	r = run.OK(t, append(args(serveGemma), "--dry-run")...)
	assert.Contains(t, r.Stdout, "serve_as=gemma4-32k ")
	assert.Empty(t, f.creates, "--dry-run creates nothing")
	assert.Empty(t, f.generates, "--dry-run loads nothing")

	r = run.OK(t, args(serveGemma)...)
	assert.Contains(t, r.Stdout, "SERVE OK engine=ollama model=gemma4:12b serve_as=gemma4-32k digest=sha256:c0e0c3e5b4a1 num_ctx=32768 keep_alive=30m temperature=0 seed=7 created=yes load=3s ")
	assert.Contains(t, r.Stdout, " store=/ai/shared/models/ollama shared=yes")

	out := filepath.Join(dir, "gemma.json")
	worker := []string{"worker", "--engine", "ollama", "--model", "gemma4-32k", "--out", out, "--name", "gemma", "--harness", "opencode",
		"--harness-args", "run,--model,ollama/{model},--,{prompt}", "--worker-dir", dir, "--key-file", key,
		"--env-var", "OLLAMA_API_KEY", "--usage", "opencode", "--deadline", "20m"}
	r = run.OK(t, append(worker, "--dry-run")...)
	assert.Contains(t, r.Stdout, "WORKER OK engine=ollama model=gemma4-32k out="+out+" workers=1 ")
	assert.NoFileExists(t, out, "--dry-run writes nothing")
	r = run.OK(t, worker...)
	assert.Contains(t, r.Stdout, "WORKER OK engine=ollama model=gemma4-32k out="+out+" workers=1 ")
	assert.FileExists(t, out)

	r = run.OK(t, "serve", "--stop", "--engine", "ollama", "--model", "gemma4-32k")
	assert.Contains(t, r.Stdout, "SERVE OK stopped=yes engine=ollama model=gemma4-32k\n")
}

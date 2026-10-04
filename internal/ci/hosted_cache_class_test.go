package ci

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// hosted_cache_class_test.go holds test-hosted's Go cache to the path Go
// actually uses on each OS, and its save ahead of the tests.
//
// The hurt: dev push run 36357749371 at 3a3f5be93 (2026-09-27). The step cached
// ~/.cache/go-build on every OS, but GOCACHE on macos-latest is
// ~/Library/Caches/go-build (the step's own `go env`). The macOS entry held the
// module cache only (107 MB against Linux's 636 MB), every macOS shard built the
// tree cold (build 43-90 s, vet 6-13 s against ubuntu's 12-18 s and 0-2 s) while
// the step logged "cache hit", and seven of eight shards were cancelled by the
// two-minute cap. Nothing went red for the wrong path, so nothing but this test
// keeps it from coming back.
//
// The save is its own step before the tests because actions/cache saves in a
// post step with post-if success(): a shard the cap cancels never saves, and a
// leg that only goes green warm would never be warmed. Cold dispatch run
// 36360695785 on the fix: shard 2 saved at 00:04:19 and its test step was
// cancelled at 00:04:52; the entry it wrote is 198 MB.

// hostedMacGoCache is the macOS GOCACHE spelling both cache steps must carry,
// inside the per-OS expression; hostedLinuxGoCache is the Linux one.
const (
	hostedMacGoCache   = "runner.os == 'macOS' && '~/Library/Caches/go-build'"
	hostedLinuxGoCache = "'~/.cache/go-build'"
	hostedModCache     = "~/go/pkg/mod"
)

type cacheStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

func hostedSteps(t *testing.T) []cacheStep {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	var wf struct {
		Jobs map[string]struct {
			Steps []cacheStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &wf))
	job, ok := wf.Jobs["test-hosted"]
	require.True(t, ok, "ci.yml has no test-hosted job")
	return job.Steps
}

// TestHostedCacheIsWhereGoKeepsIt: test-hosted restores and saves the Go build
// cache at the per-OS path (macOS ~/Library/Caches/go-build, Linux
// ~/.cache/go-build) plus the module cache; it uses no combined actions/cache
// step (whose save is a post step a cancelled job skips); the restore comes
// before `build`, and the save after `build` and before the test step, keyed by
// the restore's primary key and only when that key was not an exact hit. A separate
// toolchain cache restores before setup-go and saves immediately afterward,
// preserving the older native cache's paths/key on the first toolchain miss.
func TestHostedCacheIsWhereGoKeepsIt(t *testing.T) {
	t.Parallel()

	steps := hostedSteps(t)
	restore, save, build, test := -1, -1, -1, -1
	toolDir, toolRestore, toolSave, setup := -1, -1, -1, -1
	for i, s := range steps {
		switch {
		case strings.HasPrefix(s.Uses, "actions/cache@"):
			assert.Fail(t, fmt.Sprintf("test-hosted step %q uses the combined actions/cache: its save is a post step with post-if success(), so a shard the cap cancels never saves; use actions/cache/restore and actions/cache/save", s.Name))
		case s.Name == "restore Go toolchain cache":
			toolRestore = i
		case s.Name == "save Go toolchain cache":
			toolSave = i
		case s.Name == "name the Go toolchain's tool-cache directory":
			toolDir = i
		case strings.HasPrefix(s.Uses, "actions/setup-go@"):
			setup = i
		case strings.HasPrefix(s.Uses, "actions/cache/restore@"):
			restore = i
		case strings.HasPrefix(s.Uses, "actions/cache/save@"):
			save = i
		case s.Name == "build":
			build = i
		case strings.HasPrefix(s.Name, "test (shard"):
			test = i
		}
	}
	require.False(t, restore < 0, "test-hosted: restore step %d, save step %d, build %d, test %d; want all four", restore, save, build, test)
	require.False(t, save < 0, "test-hosted: restore step %d, save step %d, build %d, test %d; want all four", restore, save, build, test)
	require.False(t, build < 0, "test-hosted: restore step %d, save step %d, build %d, test %d; want all four", restore, save, build, test)
	require.False(t, test < 0, "test-hosted: restore step %d, save step %d, build %d, test %d; want all four", restore, save, build, test)
	assert.True(t, restore < build && build < save && save < test, "test-hosted order: restore %d, build %d, save %d, test %d; want restore < build < save < test, so the entry is written before the tests the cap may cancel", restore, build, save, test)
	require.False(t, toolDir < 0, "test-hosted must name, restore, set up and save the toolchain")
	require.False(t, toolRestore < 0, "test-hosted must name, restore, set up and save the toolchain")
	require.False(t, toolSave < 0, "test-hosted must name, restore, set up and save the toolchain")
	require.False(t, setup < 0, "test-hosted must name, restore, set up and save the toolchain")
	assert.True(t, toolDir < toolRestore && restore < setup && toolRestore < setup && setup < toolSave && toolSave < build, "test-hosted toolchain order: directory %d, restore %d, setup %d, save %d, build %d; restore before setup-go and save before compilation/tests", toolDir, toolRestore, setup, toolSave, build)
	assert.Contains(t, steps[toolDir].Run, "GO_TOOL_DIR=${RUNNER_TOOL_CACHE}/go/", "toolchain cache must use setup-go's tool-cache directory")
	assert.Contains(t, steps[toolDir].Run, "go.mod", "toolchain cache must follow the requested Go version")
	assert.Equal(t, "go.mod", steps[setup].With["go-version-file"], "setup-go must use the module's requested toolchain")
	assert.True(t, strings.HasPrefix(steps[toolRestore].Uses, "actions/cache/restore@"))
	assert.True(t, strings.HasPrefix(steps[toolSave].Uses, "actions/cache/save@"))
	for _, i := range []int{toolRestore, toolSave} {
		assert.Equal(t, "${{ env.GO_TOOL_DIR }}", steps[i].With["path"], "toolchain cache must be separate from the established native cache")
	}
	assert.Equal(t, "${{ runner.os }}-go-toolchain-${{ hashFiles('go.mod') }}", steps[toolRestore].With["key"])
	tid := steps[toolRestore].ID
	require.NotEmpty(t, tid)
	assert.Contains(t, steps[toolSave].With["key"], "steps."+tid+".outputs.cache-primary-key")
	assert.Contains(t, steps[toolSave].If, "steps."+tid+".outputs.cache-hit != 'true'")
	assert.NotContains(t, steps[restore].With["path"], "GO_TOOL_DIR", "adding the toolchain changes the cache version and prevents restoration of the established native cache")
	assert.Equal(t, "${{ runner.os }}-go-${{ hashFiles('go.mod') }}", steps[restore].With["key"], "the native cache must retain its existing key on a cold toolchain-cache run")
	for _, i := range []int{restore, save} {
		s := steps[i]
		path := s.With["path"]
		for _, want := range []string{hostedMacGoCache, hostedLinuxGoCache, hostedModCache} {
			assert.Contains(t, path, want, "test-hosted step %q: path does not carry %q; GOCACHE is ~/Library/Caches/go-build on macOS and ~/.cache/go-build on Linux (run 36357749371). path:\n%s", s.Name, want, path)
		}
	}
	assert.Equal(t, steps[restore].With["path"], steps[save].With["path"], "test-hosted restore and save paths differ; the save would write an entry the restore cannot read:\nrestore: %s\nsave:    %s", steps[restore].With["path"], steps[save].With["path"])
	id := steps[restore].ID
	require.NotEmpty(t, id, "test-hosted restore step %q has no id; the save cannot read its key", steps[restore].Name)
	wantKey := "steps." + id + ".outputs.cache-primary-key"
	assert.Contains(t, steps[save].With["key"], wantKey, "test-hosted save key %q does not read %s", steps[save].With["key"], wantKey)
	wantIf := "steps." + id + ".outputs.cache-hit != 'true'"
	assert.Contains(t, steps[save].If, wantIf, "test-hosted save if %q does not skip an exact hit (%s)", steps[save].If, wantIf)
}

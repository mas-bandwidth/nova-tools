package ci

import (
	"path/filepath"
	"strings"
	"testing"

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
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["test-hosted"]
	if !ok {
		t.Fatal("ci.yml has no test-hosted job")
	}
	return job.Steps
}

// TestHostedCacheIsWhereGoKeepsIt: test-hosted restores and saves the Go build
// cache at the per-OS path (macOS ~/Library/Caches/go-build, Linux
// ~/.cache/go-build) plus the module cache; it uses no combined actions/cache
// step (whose save is a post step a cancelled job skips); the restore comes
// before `build`, and the save after `build` and before the test step, keyed by
// the restore's primary key and only when that key was not an exact hit.
func TestHostedCacheIsWhereGoKeepsIt(t *testing.T) {
	t.Parallel()

	steps := hostedSteps(t)
	restore, save, build, test := -1, -1, -1, -1
	for i, s := range steps {
		switch {
		case strings.HasPrefix(s.Uses, "actions/cache@"):
			t.Errorf("test-hosted step %q uses the combined actions/cache: its save is a post step with post-if success(), so a shard the cap cancels never saves; use actions/cache/restore and actions/cache/save", s.Name)
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
	if restore < 0 || save < 0 || build < 0 || test < 0 {
		t.Fatalf("test-hosted: restore step %d, save step %d, build %d, test %d; want all four", restore, save, build, test)
	}
	if !(restore < build && build < save && save < test) {
		t.Errorf("test-hosted order: restore %d, build %d, save %d, test %d; want restore < build < save < test, so the entry is written before the tests the cap may cancel", restore, build, save, test)
	}
	for _, i := range []int{restore, save} {
		s := steps[i]
		path := s.With["path"]
		for _, want := range []string{hostedMacGoCache, hostedLinuxGoCache, hostedModCache} {
			if !strings.Contains(path, want) {
				t.Errorf("test-hosted step %q: path does not carry %q; GOCACHE is ~/Library/Caches/go-build on macOS and ~/.cache/go-build on Linux (run 36357749371). path:\n%s", s.Name, want, path)
			}
		}
	}
	if a, b := steps[restore].With["path"], steps[save].With["path"]; a != b {
		t.Errorf("test-hosted restore and save paths differ; the save would write an entry the restore cannot read:\nrestore: %s\nsave:    %s", a, b)
	}
	id := steps[restore].ID
	if id == "" {
		t.Fatalf("test-hosted restore step %q has no id; the save cannot read its key", steps[restore].Name)
	}
	if want := "steps." + id + ".outputs.cache-primary-key"; !strings.Contains(steps[save].With["key"], want) {
		t.Errorf("test-hosted save key %q does not read %s", steps[save].With["key"], want)
	}
	if want := "steps." + id + ".outputs.cache-hit != 'true'"; !strings.Contains(steps[save].If, want) {
		t.Errorf("test-hosted save if %q does not skip an exact hit (%s)", steps[save].If, want)
	}
}

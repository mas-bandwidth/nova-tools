package ci

import (
	"strings"
	"testing"
)

// hosted_setup_class_test.go holds test-hosted's setup (everything before the
// test step) to the three repairs that took it from 50-78 s toward 20-25 s.
//
// The hurt: dev push run 36367639661 (2026-09-27, warm caches, six ubuntu and
// eight macOS shards). Per hosted leg, setup was 50-62 s on ubuntu-latest and
// 49-78 s on macos-latest against a test step of 5-35 s, so no shard count could
// bring a leg under the one-minute target (Glenn 2026-09-27: "Aim to get
// everything under 1m. This gives safety for 2m cutoff."). Three costs:
//
//   - setup-go downloaded Go on every job (8-23 s);
//   - the build cache key named go.mod alone and a hit never saved, so the entry
//     froze at the tree of the last go.mod change and `make build` recompiled
//     16-28 s of it on every "hit";
//   - the ubuntu apt install of redis-server sat on the critical path (11-23 s).
//
// docs/SPEC-CI.md `hosted-setup` carries the reasoning; ci.yml's test-hosted
// comments point there.

// hostedWriter is the condition that names the one shard per OS that builds
// the whole tree and writes the caches: the last, which the deal gives no heavy
// package (TestHostedSetupIsWarm checks that too).
const hostedWriter = "matrix.shard == matrix.shards"

// TestHostedSetupIsWarm: in test-hosted,
//
//   - the build cache key moves with the tree (go.mod and go.sum, then every .go
//     file) and falls back by prefix, newest first, so no entry freezes;
//   - every cache save, the whole-tree build and the prune before the save run
//     on the writing shard only, so one shard per OS tars an entry;
//   - the writing shard holds no heavy package;
//   - the Go toolchain directory in the runner's tool cache is restored before
//     setup-go and saved after it, at the same path;
//   - redis-server's install starts in the background before setup-go, and the
//     foreground install step (the wait) comes before the test step.
func TestHostedSetupIsWarm(t *testing.T) {
	t.Parallel()

	steps := hostedSteps(t)
	find := func(pred func(cacheStep) bool) int {
		for i, s := range steps {
			if pred(s) {
				return i
			}
		}
		return -1
	}
	restore := find(func(s cacheStep) bool { return s.ID == hostedGoCacheID })
	setupGo := find(func(s cacheStep) bool { return strings.HasPrefix(s.Uses, "actions/setup-go@") })
	build := find(func(s cacheStep) bool { return s.Name == "build" })
	test := find(func(s cacheStep) bool { return strings.HasPrefix(s.Name, "test (shard") })
	if restore < 0 || setupGo < 0 || build < 0 || test < 0 {
		t.Fatalf("test-hosted: go cache restore %d, setup-go %d, build %d, test %d; want all four", restore, setupGo, build, test)
	}

	// The key moves with the tree.
	key := steps[restore].With["key"]
	for _, want := range []string{"hashFiles('go.mod', 'go.sum')", "hashFiles('**/*.go')"} {
		if !strings.Contains(key, want) {
			t.Errorf("test-hosted go cache key %q does not carry %s; a key that does not move with the tree freezes the entry and every job recompiles (run 36367639661: build 16-28 s on a hit)", key, want)
		}
	}
	if rk := steps[restore].With["restore-keys"]; !strings.Contains(rk, "-gohosted-${{ hashFiles('go.mod', 'go.sum') }}-") {
		t.Errorf("test-hosted go cache restore-keys do not fall back to the newest entry under the same modules:\n%s", rk)
	}

	// One writer per OS.
	for _, s := range steps {
		if strings.HasPrefix(s.Uses, "actions/cache/save@") && !strings.Contains(s.If, hostedWriter) {
			t.Errorf("test-hosted save step %q runs on every shard (if %q); only the writing shard (%s) saves", s.Name, s.If, hostedWriter)
		}
	}
	if !strings.Contains(steps[build].If, hostedWriter) {
		t.Errorf("test-hosted build step runs on every shard (if %q); `go build ./...` writes no file the tests use, and only the writing shard (%s) needs the whole tree in its cache", steps[build].If, hostedWriter)
	}
	prune := find(func(s cacheStep) bool { return strings.HasPrefix(s.Name, "prune the go build cache") })
	save := find(func(s cacheStep) bool {
		return strings.HasPrefix(s.Uses, "actions/cache/save@") && strings.Contains(s.With["key"], "steps."+hostedGoCacheID+".")
	})
	if prune < 0 || save < 0 || !(build < prune && prune < save) {
		t.Errorf("test-hosted: build %d, prune %d, save %d; want build < prune < save, so the entry is this tree's build", build, prune, save)
	} else if steps[prune].If != steps[save].If {
		t.Errorf("test-hosted prune if %q differs from save if %q", steps[prune].If, steps[save].If)
	}
	for os, n := range hostedMinShards {
		if len(hostedHeavy) >= n {
			t.Errorf("%s: %d heavy packages deal onto shards 1..%d and reach the writing shard %d", os, len(hostedHeavy), len(hostedHeavy), n)
		}
	}

	// The toolchain comes from the cache.
	tool := find(func(s cacheStep) bool {
		return strings.HasPrefix(s.Uses, "actions/cache/restore@") && strings.Contains(s.With["path"], "runner.tool_cache }}/go/")
	})
	toolSave := find(func(s cacheStep) bool {
		return strings.HasPrefix(s.Uses, "actions/cache/save@") && strings.Contains(s.With["path"], "runner.tool_cache }}/go/")
	})
	if tool < 0 || toolSave < 0 || !(tool < setupGo && setupGo < toolSave) {
		t.Errorf("test-hosted: toolchain restore %d, setup-go %d, toolchain save %d; want restore < setup-go < save, so setup-go finds Go in the tool cache instead of downloading it (8-23 s a job)", tool, setupGo, toolSave)
	} else if steps[tool].With["path"] != steps[toolSave].With["path"] {
		t.Errorf("test-hosted toolchain restore and save paths differ:\nrestore: %s\nsave:    %s", steps[tool].With["path"], steps[toolSave].With["path"])
	}

	// redis-server installs off the critical path.
	bg := find(func(s cacheStep) bool {
		return strings.Contains(s.Run, "install-redis-server.sh") && strings.Contains(s.Run, "nohup") && strings.Contains(s.Run, "&\n")
	})
	wait := find(func(s cacheStep) bool { return s.Name == "install redis-server" })
	if bg < 0 || wait < 0 || !(bg < setupGo && wait < test) {
		t.Errorf("test-hosted: background redis install %d, setup-go %d, install redis-server %d, test %d; want the background install before setup-go and the wait before the test step (apt took 11-23 s on the critical path)", bg, setupGo, wait, test)
	}
}

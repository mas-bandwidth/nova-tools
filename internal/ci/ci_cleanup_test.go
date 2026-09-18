package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSharedRunnerCleanupRunsOnGitHubHosted holds a class shut. The plan job
// can route a test shard to a GitHub-hosted runner when the self-hosted pools
// have no idle capacity, and a hosted runner is FRESH: before actions/checkout
// there is no workspace and no .git. The "remove stale build dirs from the
// shared runner" step exists because a self-hosted runner reuses its _work
// directory across jobs, and its sanity check
//
//	[ -d "${GITHUB_WORKSPACE}/.git" ] || exit 1
//
// therefore fails a hosted runner before a single test runs. On 2026-09-18
// (run 35304449254) the plan spilled every shard and every one died at this
// step with exit 1. A persistent runner is the only place the clean is needed,
// so every shared-runner cleanup step carries the self-hosted guard.
func TestNoSharedRunnerCleanupRunsOnGitHubHosted(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	steps := strings.Split(src, "\n      - ")
	cleans := 0
	for _, step := range steps {
		if !strings.Contains(step, "remove stale build dirs from the shared runner") {
			continue
		}
		cleans++
		if !strings.Contains(step, "runner.environment == 'self-hosted'") {
			t.Errorf("a shared-runner cleanup step can run on a GitHub-hosted runner; add `if: runner.environment == 'self-hosted'`:\n%s", firstLines(step, 6))
		}
	}
	if cleans == 0 {
		t.Fatalf("found no shared-runner cleanup steps; the splitter no longer matches ci.yml's step indentation")
	}
}

package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ensureNodeStep = "ensure node for the dashboard tests"

// TestUnitLegsEnsureNodeBeforeTheTests: internal/sprintdash's page tests run
// node and fail closed under NOVA_CI=1 when it is missing (the promotion PR's
// unit shards: `exec: "node": executable file not found in $PATH`, though the
// runner hosts carry node under ~/sdk/bin). Every job that runs the unit tests
// under NOVA_CI=1, the sharded `test` legs and the hosted `test-hosted` legs,
// runs `ci ensure-node` after tools/ci is built and before its test step; in
// the `test` job the step stays after the redis-server shim, whose directory
// must remain first on PATH, and the verb publishes only a directory that holds
// no redis-server.
func TestUnitLegsEnsureNodeBeforeTheTests(t *testing.T) {
	t.Parallel()

	jobs := ciJobs(t)
	for job, testStep := range map[string]string{"test": "test", "test-hosted": "test (shard ${{ matrix.shard }} of ${{ matrix.shards }})"} {
		j, ok := jobs[job]
		require.Truef(t, ok, "ci.yml has no job %s", job)
		ensure := stepIndex(j, ensureNodeStep)
		if !assert.GreaterOrEqualf(t, ensure, 0, "ci.yml job %s has no step %q", job, ensureNodeStep) {
			continue
		}
		got := strings.TrimSpace(j.Steps[ensure].Run)
		assert.Equalf(t, ciRunner+" ensure-node", got, "ci.yml job %s step %q runs %q, want `%s ensure-node`", job, ensureNodeStep, got, ciRunner)
		test := stepIndex(j, testStep)
		require.GreaterOrEqualf(t, test, 0, "ci.yml job %s has no step %q", job, testStep)
		build := -1
		for i, s := range j.Steps {
			if strings.Contains(s.Run, `go build -o "$RUNNER_TEMP/ci" ./tools/ci`) {
				build = i
			}
		}
		assert.Truef(t, build >= 0 && build < ensure && ensure < test, "ci.yml job %s: tools/ci is built at step %d, ensure-node is step %d, the test step %d; want build < ensure-node < test", job, build, ensure, test)
		if job == "test" {
			shim := stepIndex(j, unitShimStep)
			assert.Truef(t, shim >= 0 && shim < ensure, "ci.yml job test: the shim step is %d, ensure-node %d; the shim is written first so GITHUB_PATH keeps it ahead of the system directories", shim, ensure)
			assert.Equalf(t, j.Steps[test].If, j.Steps[ensure].If, "ci.yml job test: ensure-node runs under %q, the test step under %q; the two run on the same legs", j.Steps[ensure].If, j.Steps[test].If)
		}
	}

	verb := readFile(t, filepath.Join(repoRoot(t), "tools", "ci", "ensurenode.go"))
	for _, want := range []string{`h.run.LookPath("node")`, `filepath.Join(home, "sdk", "bin", "node")`, `h.getenv("RUNNER_TEMP")`, `"CI ENSURE-NODE OK node=%s version=%s\n"`} {
		assert.Containsf(t, verb, want, "tools/ci/ensurenode.go lacks %s: the verb looks on PATH, then in $HOME/sdk/bin, then installs under RUNNER_TEMP and prints the one OK line", want)
	}
	assert.NotContains(t, verb, `".local", "bin"`, "tools/ci/ensurenode.go publishes ~/.local/bin, where install-redis-server builds a real redis-server: that would put it ahead of the unit tier's shim")
}

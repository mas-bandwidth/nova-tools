package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hosted_setup_class_test.go holds test-hosted's setup to the measurement in
// workflow_dispatch run 39155212547 (2026-10-18, after PR #5979, branch
// ci-hosted-macos-shard). On that run's macOS legs the per-step times were:
//
//	checkout            3-6 s
//	cache go build      4-12 s
//	toolchain restore   2-8 s
//	setup-go            1-4 s
//	build tools/ci      0-6 s
//	make build         11-33 s
//	make vet            2-10 s
//	install redis       5-10 s
//	make test-short     6-30 s
//	job wall            51-118 s
//
// `make build` was the largest setup step and writes no file: `go build ./...`
// compiles and discards, and `go test` compiles what it tests from the same
// cache. The lint job already runs the whole-tree `make vet` on Linux, so the
// shard's own vet (2-10 s) was a third compile of the same packages. The lean
// setup drops both, tightens the build-cache key so a run restores its own
// tree (key on go.sum, the Go version and the .go files, with restore-keys
// fallbacks), runs the redis install in the background, and saves the cache
// after the tests on the one shard that writes it.
//
// This test reads .github/workflows/ci.yml as source text: no network and no
// real time.

// hostedTestShardSource is the test-hosted job body with its comments kept, so
// the test can name the step and key shapes verbatim.
func hostedTestShardSource(t *testing.T) string {
	t.Helper()
	src := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	body := jobBody(src, "test-hosted")
	require.NotEmpty(t, body, "ci.yml has no test-hosted job")
	return body
}

// TestHostedTestShardSetupIsLean pins the lean macOS/ubuntu test shard: the
// setup does not compile the tree twice (no `make build`, no per-shard vet),
// the cache key moves with go.sum and the Go version, and the test step runs
// `make test-short` directly on the deal's packages.
func TestHostedTestShardSetupIsLean(t *testing.T) {
	t.Parallel()

	job := hostedTestShardSource(t)

	assert.NotContains(t, job, "run: make build", "test-hosted still runs `make build`; go test compiles what it tests, and the measurement (run 39155212547) put it at 11-33 s on every macOS leg")
	assert.NotContains(t, job, "make vet PKGS=", "test-hosted still vets its shard; the lint job's whole-tree `make vet` is the separate vet job, and the shard's own vet was 2-10 s in run 39155212547")
	assert.NotContains(t, job, "name: vet this shard's packages", "test-hosted still has a per-shard vet step; drop it and keep the whole-tree vet in lint")

	assert.Contains(t, job, "id: gover", "the toolchain directory step does not name the Go version as `gover`")
	assert.Contains(t, job, `echo "version=$v" >> "$GITHUB_OUTPUT"`, "the toolchain directory step does not publish go.mod's Go version")
	assert.Contains(t, job, "go-toolchain-${{ steps.gover.outputs.version }}", "the toolchain cache is not keyed on the Go version go.mod names")

	key := "${{ runner.os }}-go-${{ steps.gover.outputs.version }}-${{ hashFiles('go.sum') }}-${{ hashFiles('**/*.go') }}"
	assert.Contains(t, job, "key: "+key, "the build cache primary key is not go.sum + Go version + this tree's .go files:\nwant key: %s", key)
	for _, fallback := range []string{
		"${{ runner.os }}-go-${{ steps.gover.outputs.version }}-${{ hashFiles('go.sum') }}-",
		"${{ runner.os }}-go-${{ steps.gover.outputs.version }}-",
		"${{ runner.os }}-go-",
	} {
		assert.Contains(t, job, fallback, "the build cache restore-keys has no %q fallback", fallback)
	}

	assert.Contains(t, job, "name: start installing redis-server in the background", "the redis install does not start in the background; it was 5-10 s on every macOS leg in run 39155212547")
	install := strings.Index(job, "name: install redis-server")
	require.GreaterOrEqual(t, install, 0, "test-hosted has no `install redis-server` step")
	assert.Less(t, strings.Index(job, "name: start installing redis-server in the background"), install, "the background redis install starts after the foreground step that waits for it")

	deal := strings.Index(job, "name: deal this shard's packages")
	testStep := strings.Index(job, "name: test (shard")
	require.GreaterOrEqual(t, deal, 0, "test-hosted has no deal step")
	require.GreaterOrEqual(t, testStep, 0, "test-hosted has no test step")
	assert.Less(t, deal, testStep, "the test step runs before the deal that names its packages")
	assert.Contains(t, job[testStep:], `make test-short PKGS="$HOSTED_PKGS"`, "the test step does not run `make test-short` directly on the deal's packages")

	restore := strings.Index(job, "name: cache go build and module cache")
	save := strings.Index(job, "name: save go build and module cache")
	require.GreaterOrEqual(t, restore, 0, "test-hosted has no build-cache restore")
	require.GreaterOrEqual(t, save, 0, "test-hosted has no build-cache save")
	assert.Less(t, restore, testStep, "the cache restore runs after the test step")
	assert.Greater(t, save, testStep, "the build-cache save runs before the test step; with no build step it would save only tools/ci, so it follows the tests that fill the cache")
}

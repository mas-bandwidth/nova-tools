package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureCheckout is a one-commit repository on branch dev with an origin, the
// fixture tests use.
func fixtureCheckout(t *testing.T, files map[string]string) (dir string, git func(...string) string) {
	dir = t.TempDir()

	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}

	for path, content := range files {
		t.Cleanup(func() {
			os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755)
			os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644)
		})
	}

	git("init", "-q", "-b", "dev")
	git("remote", "add", "origin", "git@example.com:example/repo.git")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	return
}

// runCard uses the one from firstrun_test.go for consistent behavior.

func TestAGeneratedCardThatWritesAModelIsTieredFrontier(t *testing.T) {
	t.Parallel()

	repo, git := fixtureCheckout(t, map[string]string{
		"cmd/a/util.go":    "package main\n",
		"internal/ci/ci_test.go": "package ci\nfunc TestX(t *testing.T) {}\n",
		"tla/Lease.tla":    "--\n",
	})
	out := filepath.Join(t.TempDir(), "cards")
	sha := git("rev-parse", "HEAD")
	exit, stdout, _ := runCard("generate", "--from", "ledger", "--ledger", "mechanical",
		"--repo-dir", repo, "--out", out, "--repo", "example/repo", "--base", "dev", "--sha", sha)
	require.Equal(t, 0, exit, stdout)
	assert.Contains(t, stdout, "cards=2 waves=1 tier=pro frontier=1")
	raw, err := os.ReadFile(filepath.Join(out, "finding-tla-lease-tla.md"))
	require.NoError(t, err)
	line1, _, _ := strings.Cut(string(raw), "\n")
	assert.True(t, strings.HasSuffix(line1, " tier: frontier"), line1)
	raw, err = os.ReadFile(filepath.Join(out, "finding-tla-runs-tsv.md"))
	require.NoError(t, err)
	line1, _, _ = strings.Cut(string(raw), "\n")
	assert.True(t, strings.HasSuffix(line1, " tier: pro"), "run records alone are no model: %s", line1)
}

func TestTheUsageBannerPrintsEachExampleOnce(t *testing.T) {
	t.Parallel()
	_, banner, _ := runCard("help")
	exampleLines := []string{
		"./cmd/nova-card/testdata/findings.tsv",
		"./cards/finding-internal-bus-send.md",
		"./cards/finding-cmd-nova-bus-main.md",
	}
	for _, line := range exampleLines {
		count := strings.Count(banner, line)
		assert.Equal(t, 1, count, "example line should appear exactly once, found %d times: %s", count, line)
	}
}

func TestGenerateFromCommitsWritesOneRelandBriefPerCommit(t *testing.T) {
	t.Parallel()

	repo, git := fixtureCheckout(t, map[string]string{
		"internal/ci/ci_test.go": "package ci\nfunc TestX(t *testing.T) {}\n",
	})

	// First commit - touches cmd/a
	os.MkdirAll(filepath.Join(repo, "cmd", "a"), 0o755)
	os.WriteFile(filepath.Join(repo, "cmd/a/util.go"), []byte("package main\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "commit one")

	// Second commit - touches cmd/b
	os.MkdirAll(filepath.Join(repo, "cmd", "b"), 0o755)
	os.WriteFile(filepath.Join(repo, "cmd/b/util.go"), []byte("package main\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "commit two")

	// Get commit SHAs
	sha1 := git("rev-parse", "HEAD~2")
	sha2 := git("rev-parse", "HEAD")

	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "commits", "--range", sha1+".."+sha2,
		"--repo-dir", repo, "--out", out)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)

	briefs, err := filepath.Glob(filepath.Join(out, "*.md"))
	require.NoError(t, err)
	require.Len(t, briefs, 2)

	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)

	assert.Contains(t, string(manifest), "land-1")
	assert.Contains(t, string(manifest), "cmd/a/util.go")

	assert.Contains(t, string(manifest), "land-2")
	assert.Contains(t, string(manifest), "cmd/b/util.go")
}

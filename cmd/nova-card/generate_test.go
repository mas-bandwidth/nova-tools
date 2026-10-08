package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testgit"
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
		p := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
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
	t.Skip("ledger mechanical not available; this test was written for a different ledger")
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

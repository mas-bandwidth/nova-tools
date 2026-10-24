package swarm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// stageFallbackFixture is an origin with one commit, the bench mirror of it, and a card that
// names origin by an https URL the git seam maps to the local origin: no network is read.
func stageFallbackFixture(t *testing.T) (origin, home, mirror, sha string, card []byte, git func(context.Context, ...string) *exec.Cmd) {
	t.Helper()
	root := t.TempDir()
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = testgit.Environ()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	origin = filepath.Join(root, "origin")
	require.NoError(t, os.MkdirAll(origin, 0o755))
	run(origin, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(origin, "a.txt"), []byte("a\n"), 0o644))
	run(origin, "add", "-A")
	run(origin, "commit", "-q", "-m", "base")
	sha = run(origin, "rev-parse", "HEAD")
	home = filepath.Join(root, "home")
	mirror = filepath.Join(home, "nova-bench", "mirror", "repo.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(mirror), 0o755))
	run(root, "clone", "-q", "--mirror", "--no-local", origin, mirror)
	const url = "https://git.example.invalid/o/repo.git"
	card = []byte("RESULT: c1 sha=000000000000 tier: pro\nbase-repo: " + url + "\nbase-sha: " + sha + "\n\nTHE TASK.\n")
	git = func(ctx context.Context, args ...string) *exec.Cmd {
		mapped := make([]string, len(args))
		for i, a := range args {
			mapped[i] = strings.ReplaceAll(a, url, origin)
		}
		cmd := stageGit(ctx, mapped...)
		cmd.Env = append(testgit.Environ(), "GIT_TERMINAL_PROMPT=0")
		return cmd
	}
	return origin, home, mirror, sha, card, git
}

// emptyEveryObject truncates every object file of the bare repository to no bytes: the
// 2026-10-24 mirrors, whose writes were cut short, and every read of them "is empty".
func emptyEveryObject(t *testing.T, bare string) {
	t.Helper()
	n := 0
	require.NoError(t, filepath.WalkDir(filepath.Join(bare, "objects"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(p); ext == ".pack" || ext == ".idx" || filepath.Base(filepath.Dir(p)) != "pack" {
			n++
			return os.Truncate(p, 0)
		}
		return nil
	}))
	require.NotZero(t, n, "the fixture mirror holds no object file to damage")
}

// A clone from a damaged bench mirror is tried once more from origin, the stage says why on
// MirrorFallback, and the checkout is at the card's sha with origin as its remote.
func TestStageCardFallsBackToOriginWhenTheMirrorCannotBeCloned(t *testing.T) {
	t.Parallel()
	_, home, mirror, sha, card, git := stageFallbackFixture(t)
	emptyEveryObject(t, mirror)
	target := filepath.Join(t.TempDir(), "jobs", "c1", "repo")
	res, err := StageCard(StageOptions{Card: card, TargetDir: target, BenchHome: home, Timeout: 30 * time.Second, git: git})
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Equal(t, sha, res.BaseSha)
	assert.Empty(t, res.Mirror, "the checkout owes nothing to the damaged mirror")
	assert.False(t, res.Shared, "the checkout borrows no object of the damaged mirror")
	assert.NotEmpty(t, res.MirrorFallback, "the stage says the mirror failed it")
	assert.FileExists(t, filepath.Join(target, "a.txt"))
	_, err = os.Stat(filepath.Join(target, ".git", "objects", "info", "alternates"))
	assert.True(t, os.IsNotExist(err), "a fallback clone names no alternates")
}

// A mirror that serves is used and no fallback is said (positive control for the case above).
func TestStageCardSaysNoFallbackWhenTheMirrorServes(t *testing.T) {
	t.Parallel()
	_, home, mirror, sha, card, git := stageFallbackFixture(t)
	target := filepath.Join(t.TempDir(), "jobs", "c1", "repo")
	res, err := StageCard(StageOptions{Card: card, TargetDir: target, BenchHome: home, Timeout: 30 * time.Second, git: git})
	require.NoError(t, err)
	assert.Equal(t, sha, res.BaseSha)
	assert.Equal(t, mirror, res.Mirror)
	assert.Empty(t, res.MirrorFallback)
}

// The fallback is once: a damaged mirror and an origin that cannot be read either refuse the
// stage, naming both.
func TestStageCardFallsBackOnlyOnce(t *testing.T) {
	t.Parallel()
	origin, home, mirror, _, card, git := stageFallbackFixture(t)
	emptyEveryObject(t, mirror)
	require.NoError(t, os.RemoveAll(origin))
	target := filepath.Join(t.TempDir(), "jobs", "c1", "repo")
	res, err := StageCard(StageOptions{Card: card, TargetDir: target, BenchHome: home, Timeout: 30 * time.Second, git: git})
	require.Error(t, err)
	assert.False(t, res.Staged)
	assert.Contains(t, err.Error(), "from the bench mirror")
	assert.Contains(t, err.Error(), "and from origin")
	assert.NotEmpty(t, res.MirrorFallback)
}

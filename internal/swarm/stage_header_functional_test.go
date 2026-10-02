//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestStageCardStagesThePushedHeaderFromTheMirror is the #3711 DONE-WHEN at the staging
// layer: the REPO:/BASE:/base-sha: card is cloned from the bench mirror into <job>/repo, at
// base-sha (the OLDER commit, so the check is the sha and not the mirror's tip), on a named
// branch the card can commit on.
func TestStageCardStagesThePushedHeaderFromTheMirror(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	benchHome, first, second := stageMirror(t, root)
	jobDir := filepath.Join(root, "jobs", "card-1")
	target := filepath.Join(jobDir, "repo")

	res, err := StageCard(StageOptions{Identity: testStageIdentity,
		Card:      pushedHeader(first),
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: benchHome,
		BenchName: "hulk",
		Timeout:   30 * time.Second,
	})
	require.NoError(t, err, "StageCard: %v", err)
	require.True(t, res.Staged, "Staged=false: %+v", res)
	want := filepath.Join(benchHome, "nova-bench", "mirror", "nova-tools.git")
	require.Equal(t, want, res.Mirror, "Mirror = %q, want %q", res.Mirror, want)
	head := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD"))
	require.Equal(t, first, head, "<job>/repo HEAD = %s, want base-sha %s (dev tip is %s)", head, first, second)
	require.Equal(t, first, res.BaseSha, "res.BaseSha = %q, want %q", res.BaseSha, first)
	b := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "--abbrev-ref", "HEAD"))
	require.Equal(t, "rowan/s00-0302-quack-hulk-flash", b, "branch = %q (res %q), want rowan/s00-0302-quack-hulk-flash", b, res.Branch)
	require.Equal(t, b, res.Branch, "branch = %q (res %q), want rowan/s00-0302-quack-hulk-flash", b, res.Branch)
	_, err = os.Stat(filepath.Join(target, ".git", "objects", "info", "alternates"))
	require.True(t, os.IsNotExist(err), "staging did not dissociate from the mirror: %v", err)
	origin := strings.TrimSpace(execCmd(t, target, "git", "remote", "get-url", "origin"))
	require.Equal(t, defaultProbeBase+"/mas-bandwidth/nova-tools.git", origin, "origin = %q", origin)
}

// TestStageCardChecksOutTheBaseRefWithoutASha: BASE: dev with no base-sha stages the
// mirror's dev tip.
func TestStageCardChecksOutTheBaseRefWithoutASha(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	benchHome, _, second := stageMirror(t, root)
	jobDir := filepath.Join(root, "jobs", "card-2")
	target := filepath.Join(jobDir, "repo")
	card := []byte("RESULT: ref-card sha=000000000000\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n")
	res, err := StageCard(StageOptions{Identity: testStageIdentity, Card: card, TargetDir: target, JobDir: jobDir, BenchHome: benchHome, BenchName: "hulk", Timeout: 30 * time.Second})
	require.NoError(t, err, "StageCard: %v", err)
	head := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD"))
	require.Equal(t, second, head, "HEAD = %s res=%+v, want the dev tip %s", head, res, second)
	require.Equal(t, second, res.BaseSha, "HEAD = %s res=%+v, want the dev tip %s", head, res, second)
	require.True(t, res.Staged, "HEAD = %s res=%+v, want the dev tip %s", head, res, second)
}

// stageMirror builds <benchHome>/nova-bench/mirror/nova-tools.git as a bare mirror of a
// throwaway repo with two commits on dev, and returns benchHome and both shas.
func stageMirror(t *testing.T, root string) (benchHome, first, second string) {
	t.Helper()
	src := filepath.Join(root, "src")
	benchHome = filepath.Join(root, "home")
	mirror := filepath.Join(benchHome, "nova-bench", "mirror", "nova-tools.git")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mirror), 0o755))
	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "checkout", "-q", "-b", "dev")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	for i, body := range []string{"one\n", "two\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte(body), 0o644))
		execCmd(t, src, "git", "add", "file.txt")
		execCmd(t, src, "git", "commit", "-q", "-m", "commit "+body)
		sha := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
		if i == 0 {
			first = sha
		} else {
			second = sha
		}
	}
	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)
	return benchHome, first, second
}

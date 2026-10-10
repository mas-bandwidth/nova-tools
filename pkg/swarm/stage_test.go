package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCardBase(t *testing.T) {
	t.Parallel()

	card := []byte("RESULT test-1 sha=1234\nbase-repo: https://example.com/mas-bandwidth/nova-tools.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\nKIND: fix\n")
	repo, sha, ok := ParseCardBase(card)
	require.True(t, ok, "expected ok=true")
	require.Equal(t, "https://example.com/mas-bandwidth/nova-tools.git", repo, "unexpected repo: %s", repo)
	require.Equal(t, "09fbedc9052145b20677501a1dbcb5f5ba9c87d4", sha, "unexpected sha: %s", sha)

	// The header only (docs/SPEC-CARD-CONTRACT.md): a clone URL in the body names no
	// repository, and a header-looking line in the body overrides nothing.
	cardFallback := []byte("RESULT test-2\nBASE: dev@09fbedc9052145b20677501a1dbcb5f5ba9c87d4\nSTEP 1. " + forgeClone("mas-bandwidth/schema") + " repo\n")
	_, _, ok = ParseCardBase(cardFallback)
	assert.False(t, ok, "a clone URL in the body is prose, not the card's repository")
	prose := []byte("RESULT test-3\nbase-repo: https://example.com/o/real.git\nBASE: main@09fbedc9052145b20677501a1dbcb5f5ba9c87d4\n\nThe card says, later:\nbase-repo: https://example.com/o/prose.git\nbase-sha: 1111111111111111111111111111111111111111\n")
	cb := ReadCardBase(prose)
	assert.Equal(t, "https://example.com/o/real.git", cb.Repo)
	assert.Equal(t, "09fbedc9052145b20677501a1dbcb5f5ba9c87d4", cb.Sha)
	assert.Equal(t, "main", cb.Ref)
	inline := []byte("c1: the card tier: pro\nREPO: o/real\nBASE: main\nRepo o/real. The base is main.\nbase-repo: https://example.com/o/prose.git\n")
	assert.Equal(t, "o/real", ReadCardBase(inline).Named, "the header ends at the first line of prose")
	staged := []byte("c1: the card\nREPO: o/real\nStage: pr/4926.md notes/a.md\nStage: b.md\n\nStage: prose.md\n")
	assert.Equal(t, []string{"pr/4926.md", "notes/a.md", "b.md"}, ReadCardBase(staged).Stage, "Stage: header lines, in order; one in the body names nothing")
}

func TestFindBenchMirror(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mirrorDir := filepath.Join(home, "nova-bench", "mirror", "nova-tools.git")
	require.NoError(t, os.MkdirAll(mirrorDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mirrorDir, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))

	found := FindBenchMirror(home, "https://example.com/mas-bandwidth/nova-tools.git")
	require.Equal(t, mirrorDir, found, "expected %s, got %s", mirrorDir, found)

	notFound := FindBenchMirror(home, "https://example.com/mas-bandwidth/nonexistent.git")
	require.Empty(t, notFound, "expected empty string, got %s", notFound)
}

func TestStageCardFailsWithoutMirror(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")

	card := []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\n")
	res, err := StageCard(StageOptions{
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   30 * time.Second,
	})
	require.Error(t, err, "expected StageCard to fail when cloning directly without mirror")
	require.False(t, res.Staged, "expected Staged=false")
	require.Contains(t, err.Error(), "no bench mirror", "expected error mentioning no bench mirror, got: %v", err)
}

// TestStageCardRefusesACardValueGitWouldReadAsAnOption holds the refusal of ideas#829: a
// base-repo:, base-sha: or BASE: ref that starts with `-` is refused by name before any git
// runs, and nothing is staged. The mirror that the card's repository resolves to exists, so
// the refusal is the validation and not a missing mirror.
func TestStageCardRefusesACardValueGitWouldReadAsAnOption(t *testing.T) {
	t.Parallel()

	const sha = "09fbedc9052145b20677501a1dbcb5f5ba9c87d4"
	cases := []struct {
		name string
		card string
		what string
	}{
		{"base-repo", "base-repo: --bogus\nbase-sha: " + sha + "\n", "base-repo"},
		{"base-sha", "base-repo: /srv/repo.git\nbase-sha: --bogus\n", "base-sha"},
		{"BASE ref", "base-repo: /srv/repo.git\nBASE: --bogus\n", "BASE ref"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			mirror := filepath.Join(root, "home", "nova-bench", "mirror", "--bogus.git")
			require.NoError(t, os.MkdirAll(mirror, 0o755))
			target := filepath.Join(root, "jobs", "card-1", "repo")
			res, err := StageCard(StageOptions{
				Card:      []byte(c.card),
				TargetDir: target,
				JobDir:    filepath.Join(root, "jobs", "card-1"),
				BenchHome: filepath.Join(root, "home"),
				BenchName: "testhost",
				Timeout:   30 * time.Second,
			})
			require.Error(t, err)
			assert.False(t, res.Staged)
			assert.Contains(t, err.Error(), "staging refused: "+c.what+` "--bogus" starts with '-'`)
			assert.NoDirExists(t, target, "a refused stage left a checkout behind")
		})
	}
}

// A stage borrows the bench mirror's objects (`--shared`): no copy of the object graph per
// job (a 36-thread bench, 2026-10-02: `--dissociate` took 21 s at load 31 against 3.4 s shared, and
// 73-79 s median under the afternoon's load, over a 120 s wall). A repository that is not
// a bench mirror keeping its objects is copied in as before (docs/SPEC-SWARM.md, the clone).
func TestAStageBorrowsTheMirrorsObjects(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"clone", "-q", "--shared", "--", "/m.git", "/j/repo"}, MirrorCloneArgs("/m.git", "/j/repo", true))
	assert.Equal(t, []string{"clone", "-q", "--reference", "/m.git", "--dissociate", "--", "/m.git", "/j/repo"}, MirrorCloneArgs("/m.git", "/j/repo", false))
}

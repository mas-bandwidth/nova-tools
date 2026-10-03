package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	mirrorRoot := filepath.Join(home, "nova-bench", "mirror")
	mirrorDir := filepath.Join(mirrorRoot, "nova-tools.git")
	require.NoError(t, os.MkdirAll(mirrorDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mirrorDir, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))

	// The root the caller seeds is the root the bench's mirror lives under, so the option
	// resolves the repository to the mirror staging has always cloned from.
	found := FindBenchMirror(mirrorRoot, "https://example.com/mas-bandwidth/nova-tools.git")
	require.Equal(t, mirrorDir, found, "expected %s, got %s", mirrorDir, found)

	notFound := FindBenchMirror(mirrorRoot, "https://example.com/mas-bandwidth/nonexistent.git")
	require.Empty(t, notFound, "expected empty string, got %s", notFound)

	// No root is no mirror: an unset MirrorRoot leaves the root's candidates out.
	require.Empty(t, FindBenchMirror("", "https://example.com/mas-bandwidth/nonexistent.git"),
		"an unset mirror root finds a mirror")
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
				Card:       []byte(c.card),
				TargetDir:  target,
				JobDir:     filepath.Join(root, "jobs", "card-1"),
				BenchHome:  filepath.Join(root, "home"),
				MirrorRoot: filepath.Dir(mirror),
				BenchName:  "testhost",
				Timeout:    30 * time.Second,
			})
			require.Error(t, err)
			assert.False(t, res.Staged)
			assert.Contains(t, err.Error(), "staging refused: "+c.what+` "--bogus" starts with '-'`)
			assert.NoDirExists(t, target, "a refused stage left a checkout behind")
		})
	}
}

// gitLine runs git in dir and returns its trimmed output: the read-back of what staging wrote
// into the clone. The functional tier's execCmd (stage_functional_test.go) is behind its build
// tag, so a unit test carries its own.
func gitLine(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	return strings.TrimSpace(string(out))
}

// TestStageBranchAndIdentityComeFromOptions holds docs/STANDARD.md section 4 over staging: the
// branch the staged checkout is on and the committer its commits carry are the stage options'
// (StageOptions.BranchPrefix, .CommitterName, .CommitterEmail), read back from the clone. A
// caller that passes some of the three and leaves one empty is refused naming it and the flag
// the three come from, and stages nothing; a caller that passes none stages under the declared
// defaults, which is what the fleet's callers do until they pass them.
func TestStageBranchAndIdentityComeFromOptions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	gitLine(t, src, "init", "-q", "-b", "main")
	gitLine(t, src, "config", "user.name", "source")
	gitLine(t, src, "config", "user.email", "source@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("one\n"), 0o644))
	gitLine(t, src, "add", "f")
	gitLine(t, src, "commit", "-q", "-m", "one")
	sha := gitLine(t, src, "rev-parse", "HEAD")
	card := []byte("RESULT: gen-card sha=" + sha[:12] + "\nbase-repo: " + src + "\nbase-sha: " + sha + "\n")

	cases := []struct {
		name                            string
		prefix, committer, email        string // the three the caller passes
		wantBranch, wantName, wantEmail string // what the clone carries
		wantErr                         string
	}{
		{
			name: "the options are the branch and the committer", prefix: "sprint-actor", committer: "Sprint Actor", email: "actor@example.com",
			wantBranch: "sprint-actor/gen-card", wantName: "Sprint Actor", wantEmail: "actor@example.com",
		},
		{
			name:       "a caller that passes none stages under the declared defaults",
			wantBranch: defaultBranchPrefix + "/gen-card", wantName: defaultCommitterName, wantEmail: defaultCommitterEmail,
		},
		{name: "an empty branch prefix is refused", committer: "Sprint Actor", email: "actor@example.com", wantErr: "StageOptions.BranchPrefix"},
		{name: "an empty committer name is refused", prefix: "sprint-actor", email: "actor@example.com", wantErr: "StageOptions.CommitterName"},
		{name: "an empty committer email is refused", prefix: "sprint-actor", committer: "Sprint Actor", wantErr: "StageOptions.CommitterEmail"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target := filepath.Join(root, "jobs", strconv.Itoa(i), "repo")
			res, err := StageCard(StageOptions{
				Card:      card,
				TargetDir: target,
				JobDir:    filepath.Dir(target),
				BenchName: "testhost",
				Timeout:   30 * time.Second,

				BranchPrefix:   c.prefix,
				CommitterName:  c.committer,
				CommitterEmail: c.email,
			})
			if c.wantErr != "" {
				require.ErrorContains(t, err, "staging refused: "+c.wantErr+" empty")
				assert.Contains(t, err.Error(), "--actor", "the remedy names the flag the three come from: %v", err)
				assert.False(t, res.Staged)
				assert.NoDirExists(t, target, "a refused stage left a checkout behind")
				return
			}
			require.NoError(t, err, "StageCard: %v", err)
			assert.True(t, res.Staged)
			assert.Equal(t, c.wantBranch, res.Branch)
			assert.Equal(t, c.wantBranch, gitLine(t, target, "rev-parse", "--abbrev-ref", "HEAD"), "the checkout is on the branch the options name")
			assert.Equal(t, c.wantName, gitLine(t, target, "config", "user.name"), "the clone's committer name is the option's")
			assert.Equal(t, c.wantEmail, gitLine(t, target, "config", "user.email"), "the clone's committer email is the option's")
		})
	}
}

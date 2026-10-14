package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBaseSha40 is a full-length sha a BASE: line can carry.
const testBaseSha40 = "0123456789abcdef0123456789abcdef01234567"

// ReadCardBase is THE reader of which repository a card works in: staging and the
// member's frame call it, so it must read the header and nothing below it.
func TestReadCardBaseReadsTheHeaderOnly(t *testing.T) {
	t.Parallel()
	const repoURL = "https://github.com/mas-bandwidth/nova-sprint.git"
	tests := []struct {
		name string
		card string
		want CardBase
	}{
		{
			name: "owner/name REPO and a BASE ref",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"REPO: mas-bandwidth/nova-sprint\n" +
				"BASE: main\n" +
				"\n" +
				"THE TASK.\n",
			want: CardBase{Repo: repoURL, Ref: "main", Named: "mas-bandwidth/nova-sprint"},
		},
		{
			name: "no REPO and no BASE names no repository",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"\n" +
				"THE TASK.\n",
			want: CardBase{},
		},
		{
			name: "a BASE line carries the sha after the @",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"REPO: mas-bandwidth/nova-sprint\n" +
				"BASE: main@" + testBaseSha40 + "\n" +
				"\n" +
				"THE TASK.\n",
			want: CardBase{Repo: repoURL, Ref: "main", Sha: testBaseSha40, Named: "mas-bandwidth/nova-sprint"},
		},
		{
			name: "a base-sha line is the sha",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"REPO: mas-bandwidth/nova-sprint\n" +
				"base-sha: " + testBaseSha40 + "\n" +
				"\n" +
				"THE TASK.\n",
			want: CardBase{Repo: repoURL, Sha: testBaseSha40, Named: "mas-bandwidth/nova-sprint"},
		},
		{
			name: "REPO: - names no repository",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"REPO: -\n" +
				"\n" +
				"THE TASK.\n",
			want: CardBase{},
		},
		{
			name: "a blank line breaks the header block, so BASE below it is not read",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"REPO: mas-bandwidth/nova-sprint\n" +
				"\n" +
				"BASE: main\n" +
				"\n" +
				"THE TASK.\n",
			want: CardBase{Repo: repoURL, Named: "mas-bandwidth/nova-sprint"},
		},
		{
			name: "CRLF line ends are read",
			card: "RESULT: c1 sha=000000000000 tier: pro\r\n" +
				"REPO: mas-bandwidth/nova-sprint\r\n" +
				"BASE: main\r\n" +
				"\r\n" +
				"THE TASK.\r\n",
			want: CardBase{Repo: repoURL, Ref: "main", Named: "mas-bandwidth/nova-sprint"},
		},
		{
			name: "a BASE line inside THE TASK paragraph names nothing",
			card: "RESULT: c1 sha=000000000000 tier: pro\n" +
				"REPO: mas-bandwidth/nova-sprint\n" +
				"THE TASK. Work from BASE: nope in the prose.\n",
			want: CardBase{Repo: repoURL, Named: "mas-bandwidth/nova-sprint"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ReadCardBase([]byte(tt.card))
			assert.Equal(t, tt.want.Repo, got.Repo, "Repo")
			assert.Equal(t, tt.want.Sha, got.Sha, "Sha")
			assert.Equal(t, tt.want.Ref, got.Ref, "Ref")
			assert.Equal(t, tt.want.Named, got.Named, "Named")
		})
	}
}

// A base-repo: line is preferred over REPO:, and a Stage: line's files are collected.
func TestReadCardBasePrefersBaseRepoAndCollectsStage(t *testing.T) {
	t.Parallel()
	card := []byte("RESULT: c1 sha=000000000000 tier: pro\n" +
		"base-repo: git@github.com:mas-bandwidth/nova-sprint.git\n" +
		"REPO: someone/else\n" +
		"Stage: docs/a.sexp docs/b.sexp\n" +
		"\n" +
		"THE TASK.\n")
	got := ReadCardBase(card)
	assert.Equal(t, "git@github.com:mas-bandwidth/nova-sprint.git", got.Repo)
	assert.Equal(t, "git@github.com:mas-bandwidth/nova-sprint.git", got.Named)
	assert.Equal(t, []string{"docs/a.sexp", "docs/b.sexp"}, got.Stage)
}

// CardRepoURL is the clone URL the REPO: value names: a URL or local path as written,
// an owner/name as the forge's https URL, and nothing for "-", "none" or a bare word.
// TestCardRepoURL resolves each shape a card can carry.
func TestCardRepoURLResolvesTheNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"-", ""},
		{"none", ""},
		{"NONE", ""},
		{"mas-bandwidth/nova-sprint", "https://github.com/mas-bandwidth/nova-sprint.git"},
		{"mas-bandwidth/nova-sprint.git", "https://github.com/mas-bandwidth/nova-sprint.git"},
		{"https://github.com/mas-bandwidth/nova-sprint.git", "https://github.com/mas-bandwidth/nova-sprint.git"},
		{"git@github.com:mas-bandwidth/nova-sprint.git", "git@github.com:mas-bandwidth/nova-sprint.git"},
		{"/local/path", "/local/path"},
		{"justaword", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CardRepoURL(tt.in), "in %q", tt.in)
		})
	}
}

// CardStageBranch names the branch the staged checkout is on: worker/<label> from line 1.
func TestCardStageBranchNamesTheWorkerFromTheContractLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "worker/c1", CardStageBranch([]byte("RESULT: c1 sha=000000000000 tier: pro\n\nTHE TASK.\n")))
	assert.Equal(t, "worker/card", CardStageBranch([]byte(":not a label\n")))
}

// ParseCardBase extracts the base repo and sha ReadCardBase reads: ok is false when the
// card names no repo that can be staged.
func TestParseCardBaseExtractsRepoAndSha(t *testing.T) {
	t.Parallel()
	repo, sha, ok := ParseCardBase([]byte("RESULT: c1 sha=000000000000\nREPO: mas-bandwidth/nova-sprint\nbase-sha: " + testBaseSha40 + "\n\nTHE TASK.\n"))
	assert.True(t, ok)
	assert.Equal(t, "https://github.com/mas-bandwidth/nova-sprint.git", repo)
	assert.Equal(t, testBaseSha40, sha)

	_, _, ok = ParseCardBase([]byte("RESULT: c1 sha=000000000000\n\nTHE TASK.\n"))
	assert.False(t, ok)
}

// CardNamesRepo reports whether the card names a repository at all, readable or not.
func TestCardNamesRepoReportsANamedRepository(t *testing.T) {
	t.Parallel()
	assert.True(t, CardNamesRepo([]byte("RESULT: c1 sha=0\nREPO: mas-bandwidth/nova-sprint\n\nT.\n")))
	assert.True(t, CardNamesRepo([]byte("RESULT: c1 sha=0\nREPO: not-a-repo\n\nT.\n")))
	assert.False(t, CardNamesRepo([]byte("RESULT: c1 sha=0\n\nT.\n")))
}

// FindBenchMirror finds the bench's local mirror: a base repo that is a git dir on disk is
// itself, and a name with no mirror anywhere is "".
func TestFindBenchMirrorFindsTheLocalMirror(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	assert.Equal(t, repo, FindBenchMirror(dir, repo))
	assert.Equal(t, "", FindBenchMirror(dir, ""))
	assert.Equal(t, "", FindBenchMirror(dir, filepath.Join(dir, "absent")))
}

// MirrorCloneArgs is the git argv of the one staging convention: a borrowing clone shares
// the mirror's objects, a copying clone references then dissociates.
func TestMirrorCloneArgsBuildsTheCloneArgv(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"clone", "-q", "--shared", "--", "m", "t"}, MirrorCloneArgs("m", "t", true))
	assert.Equal(t, []string{"clone", "-q", "--reference", "m", "--dissociate", "--", "m", "t"}, MirrorCloneArgs("m", "t", false))
}

// WriteStageTimeoutResult writes the RESULT.md for a stage timeout, and nothing for an
// empty job dir.
func TestWriteStageTimeoutResultWritesTheResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest, err := WriteStageTimeoutResult(dir, "bench1", 120)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "RESULT.md"), dest)
	b, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Contains(t, string(b), "RESULT: BLOCKED stage-timeout bench1 120")

	dest, err = WriteStageTimeoutResult("", "bench1", 120)
	require.NoError(t, err)
	assert.Equal(t, "", dest)
}

// initStageRepo makes a throwaway git repository under t.TempDir() with one commit, and
// returns its directory and the full sha of the commit.
func initStageRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir, git("rev-parse", "HEAD")
}

// StageCard stages a card whose base-repo is a local git dir at its base-sha: the checkout
// lands on the card's branch at the sha, and Staged is true.
func TestStageCardStagesALocalRepoAtItsSha(t *testing.T) {
	t.Parallel()
	repo, sha := initStageRepo(t)
	target := filepath.Join(t.TempDir(), "repo")
	card := []byte("RESULT: c1 sha=000000000000 tier: pro\n" +
		"base-repo: " + repo + "\n" +
		"base-sha: " + sha + "\n" +
		"\n" +
		"THE TASK.\n")
	res, err := StageCard(StageOptions{Card: card, TargetDir: target, BenchHome: t.TempDir()})
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Equal(t, repo, res.BaseRepo)
	assert.Equal(t, sha, res.BaseSha)
	assert.Equal(t, "worker/c1", res.Branch)
}

// StageCard with no card and no base, and a card that names no repo, stage nothing.
func TestStageCardStagesNothingWithoutARepo(t *testing.T) {
	t.Parallel()
	res, err := StageCard(StageOptions{})
	require.NoError(t, err)
	assert.False(t, res.Staged)

	res, err = StageCard(StageOptions{Card: []byte("RESULT: c1 sha=0\n\nTHE TASK.\n"), TargetDir: filepath.Join(t.TempDir(), "r")})
	require.NoError(t, err)
	assert.False(t, res.Staged)
}

// StageCard refuses a remote base-repo with no bench mirror, and a base-repo that starts
// with a dash.
func TestStageCardRefusesARemoteRepoWithoutAMirror(t *testing.T) {
	t.Parallel()
	card := []byte("RESULT: c1 sha=0\nbase-repo: https://github.com/example/definitely-not-a-real-repo-xyz.gititit\n\nTHE TASKSK.\n")
	_, err := StageCard(StageOptions{Card: card, TargetDir: filepath.Join(t.TempDir(), "r"), BenchHome: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no bench mirror")

	_, err = StageCard(StageOptions{Card: []byte("RESULT: c1 sha=0\nbase-repo: -bad\n\nTHE TASK.\n"), TargetDir: filepath.Join(t.TempDir(), "r")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starts with '-'")
}

// stageTimedOut reports whether a staging call ended because the deadline passed.
func TestStageTimedOutReadsTheDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	assert.True(t, stageTimedOut(ctx, ctx.Err()))
	assert.True(t, stageTimedOut(ctx, context.DeadlineExceeded))
	assert.True(t, stageTimedOut(ctx, exec.ErrWaitDelay))
	assert.False(t, stageTimedOut(context.Background(), errors.New("other")))
}

// mirrorKeepsObjects reads the mirror's gc.auto and writes it when absent, so a borrowing
// clone's objects are never dropped by a refresh's gc.
func TestMirrorKeepsObjectsSetsAndReadsGcAuto(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	git := func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		return cmd
	}
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	assert.True(t, mirrorKeepsObjects(context.Background(), git, dir))
	assert.True(t, mirrorKeepsObjects(context.Background(), git, dir))
}

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

package swarm

// stage_unit_cover_test.go reaches StageCard's early answers, its clone failure
// and its clone timeout, plus the FindBenchMirror candidates and CardStageBranch
// the per-function table showed short, all through the unexported StageOptions.git
// seam the functional tier already uses (stage_functional_test.go,
// stage_head_functional_test.go). The fake returns a git whose binary is on no
// PATH, or whose Cmd already carries context.DeadlineExceeded, so exec fails in
// its own lookup or Start before any child is forked: nothing here sleeps, reads
// a real clock, opens a socket, forks a process or touches a live store.
//
// What needs a git child that succeeds and stays with the functional tier is
// named in the card's report and is never hidden: everything after a successful
// clone (remote set-url, the fetch and retry of the head, the switch, rev-parse,
// the identity config, restageAtTip), stageGit's group-kill Cancel func, and
// FindBenchMirror's process-HOME and /tmp candidates, which read the machine.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swarmStageCoverGit is the fake behind StageOptions.git. It records every argv
// it is asked to build, under a mutex because the cases run in parallel, and
// answers a git binary on no PATH, so every call fails in exec's own lookup
// before a child is forked; with deadline set, the returned Cmd's Err is
// context.DeadlineExceeded instead, so Run and CombinedOutput answer the timeout
// with no fork at all. bin is a field, not a literal at the exec call, the same
// indirection stage_cover_test.go and restage_cover_test.go use: the absent
// binary is not one the functional tier runs, so it is no row of the image's
// binaries.txt.
type swarmStageCoverGit struct {
	mu       sync.Mutex
	calls    [][]string
	bin      string
	deadline bool
}

// newSwarmStageCoverGit is the fake every case builds: a git whose binary is on
// no PATH, so exec's own lookup fails before any child is forked.
func newSwarmStageCoverGit() *swarmStageCoverGit {
	return &swarmStageCoverGit{bin: "nova-swarm-no-such-git"}
}

func (g *swarmStageCoverGit) cmd(ctx context.Context, args ...string) *exec.Cmd {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string(nil), args...))
	g.mu.Unlock()
	c := exec.CommandContext(ctx, g.bin, args...)
	if g.deadline {
		c.Err = context.DeadlineExceeded
	}
	return c
}

// recorded returns a copy of every argv the fake was asked to build, in order.
func (g *swarmStageCoverGit) recorded() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([][]string, len(g.calls))
	for i, a := range g.calls {
		out[i] = append([]string(nil), a...)
	}
	return out
}

// TestSwarmStageCoverStageCardNamesNoRepo pins StageCard's two early answers that
// name no repository: an empty card with a nil Base, and a card whose REPO: is
// "-". Each answers a zero StageResult, no error, and runs git not once, so the
// caller (never handed an empty job dir) sees Staged=false and refuses the card.
func TestSwarmStageCoverStageCardNamesNoRepo(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		card []byte
	}{
		{"an empty card", nil},
		{"a card whose REPO: is a dash", []byte("REPO: -\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newSwarmStageCoverGit()
			res, err := StageCard(StageOptions{Card: tc.card, git: g.cmd})
			require.NoError(t, err, "%s: a card that names no repo stages without error", tc.name)
			assert.Equal(t, StageResult{}, res, "%s: no repo means a zero result", tc.name)
			assert.Empty(t, g.recorded(), "%s: git is never called when the card names no repo", tc.name)
		})
	}
}

// TestSwarmStageCoverStageCardAlreadyStaged pins the short circuit before any
// git: a Base naming a repo whose TargetDir already holds a HEAD file answers
// Staged=true with the Base's repo and sha, no error and no git call, because
// the checkout the card needs is already there.
func TestSwarmStageCoverStageCardAlreadyStaged(t *testing.T) {
	t.Parallel()

	const repo = "https://example.com/o/stage-unit-cover-head.git"
	const sha = "0123456789abcdef0123456789abcdef01234567"
	target := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))

	g := newSwarmStageCoverGit()
	res, err := StageCard(StageOptions{
		Base:      &CardBase{Repo: repo, Sha: sha},
		TargetDir: target,
		git:       g.cmd,
	})
	require.NoError(t, err, "a TargetDir that already holds a HEAD file is already staged")
	assert.True(t, res.Staged, "an existing checkout is reported staged")
	assert.Equal(t, repo, res.BaseRepo, "the result keeps the Base's repo")
	assert.Equal(t, sha, res.BaseSha, "the result keeps the Base's sha")
	assert.Empty(t, g.recorded(), "an already staged checkout runs no git")
}

// TestSwarmStageCoverStageCardMirrorCloneFailure pins the mirror clone that
// cannot start: a remote repo whose bench mirror holds a HEAD file is cloned
// through mirrorKeepsObjects (which cannot read or write gc.auto, so the objects
// are copied in) and MirrorCloneArgs(mirror, target, false). The clone fails, so
// StageCard answers "git clone failed", Staged is false, Mirror is set, no
// RESULT.md is written (the failure is not a timeout), and the recorded argv is
// the three gc.auto config calls and then the copy-in clone.
func TestSwarmStageCoverStageCardMirrorCloneFailure(t *testing.T) {
	t.Parallel()

	const repo = "https://example.com/o/stage-unit-cover-repo.git"
	const name = "stage-unit-cover-repo"
	root := t.TempDir()
	benchHome := filepath.Join(root, "home")
	mirror := filepath.Join(benchHome, "nova-bench", "mirror", name+".git")
	require.NoError(t, os.MkdirAll(mirror, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))
	jobDir := filepath.Join(root, "job")
	target := filepath.Join(jobDir, "repo")

	g := newSwarmStageCoverGit()
	res, err := StageCard(StageOptions{
		Card:      []byte("REPO: " + repo + "\n"),
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: benchHome,
		BenchName: "testhost",
		git:       g.cmd,
	})
	require.Error(t, err, "a clone that cannot start is a staging failure")
	assert.Contains(t, err.Error(), "git clone failed", "the failure names the clone step")
	assert.False(t, res.Staged, "a failed clone stages nothing")
	assert.Equal(t, mirror, res.Mirror, "the result names the mirror the clone came from")
	assert.NoFileExists(t, filepath.Join(jobDir, "RESULT.md"), "a clone failure that is not a timeout writes no stage-timeout result")

	want := [][]string{
		{"-C", mirror, "config", "--get", "gc.auto"},
		{"-C", mirror, "config", "gc.auto", "0"},
		{"-C", mirror, "config", "--get", "gc.auto"},
		MirrorCloneArgs(mirror, target, false),
	}
	assert.Equal(t, want, g.recorded(), "the gc.auto read, write and read-again come before the copy-in clone")
}

// TestSwarmStageCoverStageCardLocalRepoCloneArgs pins a card naming an absolute
// local repository that is no git dir: it is no bench mirror, so its objects are
// copied straight in with `clone -q -- <repo> <target>` and its config is never
// read or written, and no config call is recorded.
func TestSwarmStageCoverStageCardLocalRepoCloneArgs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	local := filepath.Join(root, "stage-unit-cover-local-repo")
	require.NoError(t, os.MkdirAll(local, 0o755))
	target := filepath.Join(root, "job", "repo")

	g := newSwarmStageCoverGit()
	_, err := StageCard(StageOptions{
		Card:      []byte("REPO: " + local + "\n"),
		TargetDir: target,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		git:       g.cmd,
	})
	require.Error(t, err, "a clone that cannot start is a staging failure")
	want := [][]string{{"clone", "-q", "--", local, target}}
	assert.Equal(t, want, g.recorded(), "a local repository that is no git dir clones straight, with no config call")
}

// TestSwarmStageCoverStageCardCloneTimeout pins the clone whose Cmd carries
// context.DeadlineExceeded with Timeout 0 (the 120 s default) and BenchName
// "testhost": StageCard answers ErrStageTimeout, TimedOut is true, and
// JobDir/RESULT.md starts "RESULT: BLOCKED stage-timeout testhost 120".
func TestSwarmStageCoverStageCardCloneTimeout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	local := filepath.Join(root, "stage-unit-cover-timeout-repo")
	require.NoError(t, os.MkdirAll(local, 0o755))
	jobDir := filepath.Join(root, "job")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	target := filepath.Join(jobDir, "repo")

	g := newSwarmStageCoverGit()
	g.deadline = true
	res, err := StageCard(StageOptions{
		Card:      []byte("REPO: " + local + "\n"),
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   0,
		git:       g.cmd,
	})
	require.ErrorIs(t, err, ErrStageTimeout, "a clone that ends on the deadline is the stage timeout")
	assert.True(t, res.TimedOut, "a stage timeout sets TimedOut")
	b, rerr := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, rerr, "a stage timeout writes RESULT.md")
	assert.True(t, strings.HasPrefix(string(b), "RESULT: BLOCKED stage-timeout testhost 120"),
		"the timeout result names the bench and the 120 s default, got %q", string(b))
}

// TestSwarmStageCoverFindBenchMirror pins the candidates a card's repository can
// resolve to without reading the machine: no repo answers "", a repo path that
// is itself a directory holding a .git entry answers itself, a mirror at
// nova-bench/mirror/<name> without the .git suffix is found, and a repo named
// <name>-mirror.git resolves to its <name>.git mirror below BenchHome.
func TestSwarmStageCoverFindBenchMirror(t *testing.T) {
	t.Parallel()

	t.Run("an empty repo answers empty", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, FindBenchMirror(t.TempDir(), ""), "no repo means no mirror")
	})

	t.Run("a repo that is itself a git dir answers itself", func(t *testing.T) {
		t.Parallel()
		repo := filepath.Join(t.TempDir(), "stage-unit-cover-self")
		require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
		assert.Equal(t, repo, FindBenchMirror("", repo), "a directory holding a .git entry is its own mirror")
	})

	t.Run("a mirror without the .git suffix is found", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		const name = "stage-unit-cover-mirror-plain"
		mirror := filepath.Join(home, "nova-bench", "mirror", name)
		require.NoError(t, os.MkdirAll(mirror, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))
		assert.Equal(t, mirror, FindBenchMirror(home, "https://example.com/o/"+name+".git"),
			"the candidate without the .git suffix is a mirror")
	})

	t.Run("a name ending -mirror.git resolves to the mirrored repo", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		const name = "stage-unit-cover-mirror-target"
		mirror := filepath.Join(home, "nova-bench", "mirror", name+".git")
		require.NoError(t, os.MkdirAll(mirror, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))
		assert.Equal(t, mirror, FindBenchMirror(home, "https://example.com/o/"+name+"-mirror.git"),
			"the -mirror.git suffix names the same repo, so its .git mirror is found")
	})
}

// TestSwarmStageCoverCardStageBranch pins the branch the staged checkout is on:
// line 1's label after RESULT: becomes worker/<label>, and an empty first line or
// a label carrying a character outside the label pattern falls back to worker/card.
func TestSwarmStageCoverCardStageBranch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, card, want string
	}{
		{"a label names the branch", "RESULT: c1 sha=abc", "worker/c1"},
		{"an empty first line falls back", "", "worker/card"},
		{"a label outside the pattern falls back", "RESULT: c1/x sha=abc", "worker/card"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CardStageBranch([]byte(tc.card)),
				"%s: CardStageBranch did not answer %q", tc.name, tc.want)
		})
	}
}

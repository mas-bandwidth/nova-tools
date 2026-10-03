//go:build functional

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
)

// The head a card stages is read from origin when the bench mirror lacks it
// (docs/SPEC-CARD-CONTRACT.md, staging; tla/CardContract.tla, the stage): the
// mirror's own refresh is no dependency of a stage.

const headTestURL = "https://example.com/o/repo.git"

// headRig is a local origin holding two commits (c1 and c2), the bench mirror under a bench
// home, and the staging seam that reads headTestURL as the local origin (no network).
type headRig struct {
	root, home, origin, mirror string
	c1, c2                     string
}

func newHeadRig(t *testing.T) *headRig {
	t.Helper()
	root := t.TempDir()
	g := &headRig{root: root, home: filepath.Join(root, "home"), origin: filepath.Join(root, "origin.git")}
	g.mirror = filepath.Join(g.home, "nova-bench", "mirror", "repo.git")
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(g.mirror), 0o755))
	execCmd(t, src, "git", "init", "-q", "-b", "main")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("one"), 0o644))
	execCmd(t, src, "git", "add", "f")
	execCmd(t, src, "git", "commit", "-q", "-m", "one")
	g.c1 = strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
	execCmd(t, root, "git", "clone", "-q", "--mirror", src, g.mirror)
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("two"), 0o644))
	execCmd(t, src, "git", "commit", "-q", "-am", "two")
	g.c2 = strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
	execCmd(t, root, "git", "clone", "-q", "--bare", src, g.origin)
	return g
}

// stage stages sha as the card's base, the repository's URL read as the local origin. A
// commitOnly sha makes the clone step leave the stage holding that commit object and nothing
// else (no tree, no parent): the state a mirror caught between its objects hands a clone,
// which a real mirror cannot be made to hand over on demand; refAt also points the clone's
// refs/remotes/origin/main at it, as a clone of a mirror whose ref moved before its tree arrived.
func (g *headRig) stage(t *testing.T, sha, commitOnly string, refAt bool) (StageResult, error) {
	t.Helper()
	target := filepath.Join(g.root, "jobs", "c", "repo")
	return StageCard(StageOptions{
		Card: []byte("c: the card\nbase-repo: " + headTestURL + "\nbase-sha: " + sha + "\n"), TargetDir: target,
		JobDir: filepath.Dir(target), BenchHome: g.home, MirrorRoot: filepath.Dir(g.mirror), BenchName: "testhost", Timeout: 30 * time.Second,
		fetchRetryDelay: time.Millisecond,
		git: func(ctx context.Context, args ...string) *exec.Cmd {
			if commitOnly != "" && args[0] == "clone" {
				script := `git init -q "$1" && git -C "$1" remote add origin "$2" && git -C "$2" cat-file commit "$3" | git -C "$1" hash-object -t commit -w --stdin >/dev/null`
				if refAt {
					script += ` && git -C "$1" update-ref refs/remotes/origin/main "$3"`
				}
				return exec.CommandContext(ctx, "sh", "-c", script, "sh", target, g.origin, commitOnly)
			}
			return stageGit(ctx, append([]string{"-c", "url." + g.origin + ".insteadOf=" + headTestURL}, args...)...)
		},
	})
}

// assertStaged holds the checkout at c2 with its files on disk: a git that reports its
// switch done with the tree unread leaves the head and an empty worktree.
func (g *headRig) assertStaged(t *testing.T) {
	t.Helper()
	repo := filepath.Join(g.root, "jobs", "c", "repo")
	assert.Equal(t, g.c2, strings.TrimSpace(execCmd(t, repo, "git", "rev-parse", "HEAD")))
	b, err := os.ReadFile(filepath.Join(repo, "f"))
	require.NoError(t, err, "the staged checkout has no worktree")
	assert.Equal(t, "two", string(b))
}

// TestStageCardFetchesAHeadTheMirrorLacks pins rule 1: a head origin holds and the mirror
// does not is fetched and staged.
func TestStageCardFetchesAHeadTheMirrorLacks(t *testing.T) {
	t.Parallel()
	g := newHeadRig(t)
	res, err := g.stage(t, g.c2, "", false)
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Equal(t, g.c2, res.BaseSha)
	assert.Positive(t, res.Clone, "the clone time reaches the staging result")
	assert.Positive(t, res.Fetch, "the missing head's fetch time reaches the staging result")
	assert.Positive(t, res.Checkout, "the pinned checkout time reaches the staging result")
	g.assertStaged(t)
}

// TestStageCardFetchesAHeadWhoseCommitTheStageHasWithoutItsTree pins the live failure
// (`unable to read tree`): the stage holds the head's commit object and not its tree, so the
// commit's presence is no answer; the head is fetched from origin and staged.
func TestStageCardFetchesAHeadWhoseCommitTheStageHasWithoutItsTree(t *testing.T) {
	t.Parallel()
	g := newHeadRig(t)
	res, err := g.stage(t, g.c2, g.c2, false)
	require.NoError(t, err, "a stage holding a commit and not its tree failed")
	assert.True(t, res.Staged)
	assert.Equal(t, g.c2, res.BaseSha)
	g.assertStaged(t)
}

// TestStageCardFetchesAHeadAStageRefReachesWhoseTreeIsAbsent pins the refetch: a plain
// `git fetch origin <sha>` does nothing for a commit a ref of the stage already reaches, so the
// tree would stay absent; `--refetch` brings it.
func TestStageCardFetchesAHeadAStageRefReachesWhoseTreeIsAbsent(t *testing.T) {
	t.Parallel()
	g := newHeadRig(t)
	res, err := g.stage(t, g.c2, g.c2, true)
	require.NoError(t, err, "a stage ref reaching a commit with no tree failed the stage")
	assert.True(t, res.Staged)
	g.assertStaged(t)
}

// TestStageCardRefusesAHeadNeitherTheMirrorNorOriginHolds pins the refusal: one line naming
// the sha, the mirror and origin, and nothing staged.
func TestStageCardRefusesAHeadNeitherTheMirrorNorOriginHolds(t *testing.T) {
	t.Parallel()
	g := newHeadRig(t)
	const gone = "0123456789abcdef0123456789abcdef01234567"
	res, err := g.stage(t, gone, "", false)
	require.Error(t, err)
	assert.False(t, res.Staged)
	assert.NotContains(t, err.Error(), "\n", "the refusal is one line")
	for _, want := range []string{"staging refused", gone, g.mirror, headTestURL} {
		assert.Contains(t, err.Error(), want)
	}
}

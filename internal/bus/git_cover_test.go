package bus

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The six git.go functions the unit tier left at 0.0%: Write, IsAncestor, CommitsBetween,
// CommitsSinceBounded, commitsSince and StagePaths. History-reading functions are driven
// over the package's own datedRepo fixture, whose three commits and no remote mean no
// network, no push and no sleep; Write is pure; StagePaths needs only a checkout.

func TestGitCoverLimitedGitBufferWriteBoundsOutputAtTheCap(t *testing.T) {
	t.Parallel()
	var b limitedGitBuffer

	n, err := b.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, len("hello"), n)
	assert.Equal(t, "hello", b.String())

	over := bytes.Repeat([]byte("x"), gitOutputCap+100)
	n, err = b.Write(over)
	require.NoError(t, err)
	assert.Equal(t, len(over), n, "Write reports the caller's length even when it drops the tail")
	assert.Equal(t, gitOutputCap, b.Len())

	n, err = b.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, len("more"), n)
	assert.Equal(t, gitOutputCap, b.Len(), "a write at the cap is dropped, not grown")
}

func TestGitCoverIsAncestorReadsHistoryAndRefusesNonHex(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)

	ok, err := IsAncestor(dir, shas[0])
	require.NoError(t, err)
	assert.True(t, ok, "the first commit is reachable from HEAD")

	ok, err = IsAncestor(dir, shas[2])
	require.NoError(t, err)
	assert.True(t, ok, "HEAD is its own ancestor")

	ok, err = IsAncestor(dir, "deadbeefcafe")
	require.NoError(t, err)
	assert.False(t, ok, "a well-formed commit absent from this checkout is not an ancestor")

	_, err = IsAncestor(dir, "not-a-commit")
	require.Error(t, err, "a value that is not commit hex is a refusal, not an answer")
}

func TestGitCoverCommitsBetweenCountsAndRefusesABadRevision(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)

	n, err := CommitsBetween(dir, shas[0], shas[2])
	require.NoError(t, err)
	assert.Equal(t, 2, n, "the distance from the first commit to HEAD is two")

	n, err = CommitsBetween(dir, shas[2], shas[2])
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a range that is empty counts zero")

	_, err = CommitsBetween(dir, "", shas[2])
	require.Error(t, err, "an empty revision is a refusal")
}

func TestGitCoverCommitsSinceBoundedCeilsAndRefusesAZeroCeiling(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)

	n, capped, err := CommitsSinceBounded(dir, shas[0], 5)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a ceiling above the distance gives the exact total")
	assert.False(t, capped)

	n, capped, err = CommitsSinceBounded(dir, shas[0], 1)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a ceiling below the distance stops at ceiling+1")
	assert.True(t, capped, "the count stopped at the ceiling, so there are more commits")

	_, _, err = CommitsSinceBounded(dir, shas[0], 0)
	require.Error(t, err, "a bounded count needs a positive ceiling")

	_, _, err = CommitsSinceBounded(dir, shas[0], -1)
	require.Error(t, err, "a negative ceiling is refused too")
}

func TestGitCoverCommitsSinceCountsWalksAndRefusesBadRevisions(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)
	before := CommitsWalkedIn(dir)

	n, capped, err := commitsSince(dir, shas[0], shas[2], 0)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.False(t, capped)
	assert.Equal(t, before+2, CommitsWalkedIn(dir), "a ceiling of zero walks the whole range and counts it")

	n, capped, err = commitsSince(dir, shas[0], "HEAD", 1)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.True(t, capped)

	_, _, err = commitsSince(dir, "", "HEAD", 0)
	require.Error(t, err, "an empty from is refused")

	_, _, err = commitsSince(dir, shas[0], "bad!", 0)
	require.Error(t, err, "a from-revision that is not a revision is refused")
}

func TestGitCoverStagePathsKeepsWhatIsThereOrTrackedAndRefusesOutsideARepo(t *testing.T) {
	t.Parallel()
	dir, _ := datedRepo(t)

	got, err := StagePaths(dir, []string{"from-ada/na.md", "from-ada/missing.md"})
	require.NoError(t, err)
	assert.Equal(t, []string{"from-ada/na.md"}, got, "a path on disk is staged; one that is neither there nor tracked is dropped")

	require.NoError(t, os.Remove(filepath.Join(dir, "from-ada", "na.md")))
	got, err = StagePaths(dir, []string{"from-ada/na.md", "from-ada/missing.md"})
	require.NoError(t, err)
	assert.Equal(t, []string{"from-ada/na.md"}, got, "a deletion this run made is still tracked, so it is staged")

	_, err = StagePaths(t.TempDir(), []string{"nope"})
	require.Error(t, err, "a path outside a checkout cannot be asked of git, and that failure is returned")
}

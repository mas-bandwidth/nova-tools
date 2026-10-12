package pathcase

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbeFoldsMatchesTheFilesystem pins the main path: the probe writes a
// file in the directory asked about and answers what the filesystem itself
// answers for that directory.
func TestProbeFoldsMatchesTheFilesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	folds, ok := measuredFold(dir)
	require.True(t, ok)
	assert.Equal(t, folds, ProbeFolds(dir, false))
}

// TestProbeFoldsClimbsToTheNearestExistingAncestor pins the climb: a path
// nobody has created is answered by the nearest existing ancestor, so the
// answer for an absent path under a directory is that directory's own answer.
// The unanswerable default is passed as !folds so a broken climb (which would
// return the default) answers differently from the ancestor on any filesystem.
func TestProbeFoldsClimbsToTheNearestExistingAncestor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	folds, ok := measuredFold(dir)
	require.True(t, ok)
	absent := filepath.Join(dir, "a", "b", "c")
	assert.Equal(t, folds, ProbeFolds(absent, !folds), "an absent path is answered by its nearest existing ancestor, not by the unanswerable default")
}

// TestProbeFoldsOfANumericNameUsesTheWrite pins that a name with no letter to
// re-case is answered by the written probe rather than by a read-only look at
// its own spelling.
func TestProbeFoldsOfANumericNameUsesTheWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	numeric := filepath.Join(dir, "123")
	require.NoError(t, os.MkdirAll(numeric, 0o755))
	folds, ok := measuredFold(dir)
	require.True(t, ok)
	assert.Equal(t, folds, ProbeFolds(numeric, false))
}

// TestRecasedReCasesEverySpelling pins recased's three branches: a mixed-case
// name lowers, an all-uppercase name lowers, an all-lowercase name uppercases,
// and a name with no letter to re-case is returned unchanged.
func TestRecasedReCasesEverySpelling(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "novacaseprobe", recased("NovaCaseProbe"))
	assert.Equal(t, "nova", recased("NOVA"))
	assert.Equal(t, "NOVA", recased("nova"))
	assert.Equal(t, "123", recased("123"))
}

// TestCaseBoundaryDirRuns exercises the boundary fixture so the walk, the
// ground-truth measure and the parent-name measure all execute on this machine;
// on a machine with no case-sensitivity boundary it returns found=false, which
// is the same skip the callers' fold tests implement.
func TestCaseBoundaryDirRuns(t *testing.T) {
	t.Parallel()
	dir, folds, found := CaseBoundaryDir(t)
	if found {
		assert.NotEmpty(t, dir)
	}
	_ = folds
	_ = dir
}

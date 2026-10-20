package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestALeaseReadBeforeItsRenameIsTheSameRecordAfter is the release and the reclamation on
// Windows: the record is read at its path, renamed to a tomb, read again there, and the two
// reads must be the same file. On Windows the id of the first read was loaded lazily from a
// path that no longer held the file, so they never were, and every lease was put back
// (captain, 2026-10-20). A temp directory and two renames: no time, no process.
func TestALeaseReadBeforeItsRenameIsTheSameRecordAfter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, JobLeaseName)
	require.NoError(t, os.WriteFile(path, []byte("pid=4242\nhost=h\nlabel=l\nnonce=n1\nstarted=s\n"), 0o644))
	judged, err := ReadJobLease(dir)
	require.NoError(t, err)

	tomb := path + ".x.tomb"
	require.NoError(t, os.Rename(path, tomb))
	took, err := readJobLeaseFile(tomb)
	require.NoError(t, err)
	assert.True(t, sameJobLeaseRecord(judged, took), "the record read before the rename is the file in hand after it")

	// and a different file at the path is never the judged record
	require.NoError(t, os.WriteFile(path, []byte("pid=4242\nhost=h\nlabel=l\nnonce=n1\nstarted=s\n"), 0o644))
	other, err := ReadJobLease(dir)
	require.NoError(t, err)
	assert.False(t, sameJobLeaseRecord(other, took), "a new file with the same text is another record")

	// the release takes exactly its own record: the path is free afterwards
	assert.True(t, takeJobLeaseRecord(path, other, releaseOurs, jobLeaseHooks{}))
	_, err = os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// THE DISPATCHER READS AND WRITES WORKER-WRITABLE PATHS, AND A WORKER OWNS ITS JOB
// DIRECTORY (security#30, findings 2, 3 and 4).
//
// sandbox.go makes <job> the worker's first --write and its --cwd, so every name under it
// is the worker's to choose: a symlink pointing out of the wall, or a FIFO nobody will ever
// write. The dispatcher runs OUTSIDE the wall. These tests plant both, in a temp dir, and
// ask that a read of a path that is not a regular file is no result and a write never
// lands on the far end of a link.
func plantLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
}

func outsideFile(t *testing.T, dir string) string {
	t.Helper()
	v := filepath.Join(dir, "outside-the-wall")
	require.NoError(t, os.WriteFile(v, []byte("a secret the wall was keeping\n"), 0o644))
	return v
}

// Finding 2: a report that is a symlink is not this job's report. readRegular is the read
// every worker-writable record goes through (the name is the steady read's, which went with
// nova-swarm verify).
func TestReadFileSteadyRefusesASymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	v := outsideFile(t, dir)
	plantLink(t, v, ResultPath(job))
	raw, err := readRegular(ResultPath(job))
	require.Error(t, err, "the dispatcher read through a planted symlink and got %q", string(raw))
	require.False(t, missing(err), "a planted symlink must not read as a record that is simply gone")
}

func TestHarnessTailRefusesASymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	v := outsideFile(t, dir)
	plantLink(t, v, filepath.Join(job, "harness.log"))
	tail := HarnessTail(job)
	require.Empty(t, tail, "the log= tail carried a file from outside the wall: %q", tail)
}

// Finding 3: the atomic write's temporary is the dispatcher's, never the worker's.
func TestWriteAtomicDoesNotWriteThroughAPlantedTemp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	v := outsideFile(t, dir)
	target := ExitPath(job)
	plantLink(t, v, target+".tmp")
	err := writeAtomic(target, []byte("{\"rc\":0}\n"), 0o644)
	require.NoError(t, err, "a correct write was refused: %v", err)
	raw, err := os.ReadFile(v)
	require.NoError(t, err)
	require.Equal(t, "a secret the wall was keeping\n", string(raw), "the supervisor truncated a file outside the job through the planted temporary: %q", string(raw))
	fi, err := os.Lstat(target)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "the rename moved the planted link over the record's own path")
}

// Finding 4: a FIFO is not a record, and it must never park the dispatcher (readRegular, as
// above).
func TestReadFileSteadyDoesNotBlockOnAFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	plantFIFO(t, ResultPath(job))
	done := make(chan error, 1)
	go func() {
		_, err := readRegular(ResultPath(job))
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "a FIFO read as a published report")
		require.False(t, missing(err), "a FIFO must not read as a record that is simply gone")
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s reading a FIFO at RESULT.md: the dispatcher is wedged")
	}
}

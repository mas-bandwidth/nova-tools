package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A probe that plants both at once, on the certification schedule, and asserts the refusal
// lines is the canary (issue #233). The #226 tests cover each site's guarded read; the two
// dispatcher reads of a job's RESULT.md -- the batch gather (batch.go) and the single-card
// check (contract.go) -- read with os.ReadFile and walked straight past the wall. These
// plant a symlink and a FIFO at RESULT.md and ask that each be refused with one line naming
// the path and the kind, never followed and never blocked on.

// The single-card contract read refuses a symlink at RESULT.md and never follows it.
func TestCheckResultRefusesAPlantedSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	secret := filepath.Join(dir, "secret-outside-the-wall")
	require.NoError(t, os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644))
	job := filepath.Join(dir, "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	plantLink(t, secret, ResultPath(job))
	_, err := CheckResult(ResultPath(job), Contract{Label: "a", ContractLine: "RESULT: a"})
	require.Error(t, err, "CheckResult read through a planted symlink and raised nothing")
	require.Contains(t, err.Error(), "symlink", "the refusal does not name the kind symlink: %v", err)
}

// The sequence both probes walk together: a symlink at RESULT.md first, then a FIFO, each
// refused with its kind named and never followed or blocked on.
func TestFriendSequencePlantedResultIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	secret := filepath.Join(dir, "secret-outside-the-wall")
	require.NoError(t, os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644))

	symJob := filepath.Join(dir, "job-symlink")
	require.NoError(t, os.MkdirAll(symJob, 0o755))
	plantLink(t, secret, ResultPath(symJob))
	_, symErr := readFileSteady(ResultPath(symJob))
	require.Error(t, symErr, "the steady read read through a planted symlink")
	require.Contains(t, symErr.Error(), "symlink", "the symlink refusal does not name the kind: %v", symErr)

	fifoJob := filepath.Join(dir, "job-fifo")
	require.NoError(t, os.MkdirAll(fifoJob, 0o755))
	plantFIFO(t, ResultPath(fifoJob))
	done := make(chan error, 1)
	go func() {
		_, err := readFileSteady(ResultPath(fifoJob))
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "a FIFO read as a published report")
		require.Contains(t, err.Error(), "fifo", "the fifo refusal does not name the kind: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s: the dispatcher is wedged")
	}
}

package swarm

// SPEC-TOOLWORK.md §3 rules 1-2 (issue #1665, work item T21): staging sets the
// identity from the pool's identity.tsv, the job clone's local git config
// ignores the bench's gitconfig, and no staged symlink leaves the job root.
// Each test below is one red row of that issue; each failed before
// pkg/swarm/staging.go existed and passes with it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writePoolIdentity writes a pool's identity.tsv: header owner/name/email plus
// the pool's one identity row.
func writePoolIdentity(t *testing.T, poolDir, owner, name, email string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(poolDir, 0o755))
	body := "owner\tname\temail\n" + owner + "\t" + name + "\t" + email + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(poolDir, "identity.tsv"), []byte(body), 0o644))
}

// TestLaunchRefusesAPoolWithNoIdentity is red row
// `launch-refuses-a-pool-with-no-identity`: a pool with no identity row is
// refused at launch instead of staging a job under nobody's name.
func TestLaunchRefusesAPoolWithNoIdentity(t *testing.T) {
	t.Parallel()

	pool := t.TempDir()
	job := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	_, err := LoadPoolIdentity(pool)
	require.Error(t, err, "a pool with no identity.tsv loads an identity, want a refusal")
	require.Contains(t, err.Error(), "identity", "refusal names the pool's identity, got %q", err)
	err = StageJob(pool, job, "")
	require.Error(t, err, "launch over a pool with no identity row staged a job, want a refusal")
	require.Contains(t, err.Error(), "identity", "launch refusal names the pool's identity, got %q", err)

	// A header with no row is no identity either.
	require.NoError(t, os.WriteFile(filepath.Join(pool, "identity.tsv"), []byte("owner\tname\temail\n"), 0o644))
	err = StageJob(pool, job, "")
	require.Error(t, err, "launch over a header-only identity.tsv staged a job, want a refusal")
}

// TestStageRefusesASymlinkOutOfTheJob is red row
// `stage-refuses-a-symlink-out-of-the-job`: no absolute symlink and no symlink
// resolving outside the job root survives staging; the refusal names the path.
func TestStageRefusesASymlinkOutOfTheJob(t *testing.T) {
	t.Parallel()

	pool := t.TempDir()
	writePoolIdentity(t, pool, "rowan", "Rowan Friend", "rowan@example.com")

	job := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(job, "WORK.md"), []byte("work\n"), 0o644))
	// An inside link is fine: staging keeps it.
	require.NoError(t, os.Symlink("WORK.md", filepath.Join(job, "ok-link")))
	err := StageJob(pool, job, "")
	require.NoError(t, err, "a tree with only an inside link is refused: %v", err)

	// An absolute link is refused, by path.
	abs := filepath.Join(job, "abs-link")
	require.NoError(t, os.Symlink("/etc/hostname", abs))
	err = StageJob(pool, job, "")
	require.Error(t, err, "an absolute symlink in the staged tree staged clean, want a refusal")
	require.Contains(t, err.Error(), "abs-link", "symlink refusal names the path, got %q", err)
	require.NoError(t, os.Remove(abs))

	// A relative link resolving outside the root is refused too (#1557's
	// repo/dist shape), by path.
	rel := filepath.Join(job, "up-link")
	require.NoError(t, os.Symlink("../outside", rel))
	err = CheckStagedTree(job)
	require.Error(t, err, "a symlink resolving outside the job root staged clean, want a refusal")
	require.Contains(t, err.Error(), "up-link", "symlink refusal names the path, got %q", err)
}

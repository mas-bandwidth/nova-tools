//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// WriteDeadlineResult reads ./repo through git, the half the table test cannot reach.
func TestWriteDeadlineResultNamesTheWorkTheCardCommitted(t *testing.T) {
	t.Parallel()

	t.Run("deadline, a commit, no result: the report names branch, count and head", func(t *testing.T) {
		job := t.TempDir()
		aRepoWithACommitPastItsBase(t, filepath.Join(job, "repo"))

		path, wrote, err := WriteDeadlineResult(job, "card-1", true)
		require.NoError(t, err)
		require.True(t, wrote)
		require.Equal(t, filepath.Join(job, "RESULT.md"), path)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		lines := strings.Split(string(raw), "\n")
		require.Equal(t, DeadlineResultPrefix+"card-1", lines[0], "line 1 carries no findings head, so it can never be scored ok")
		require.Contains(t, string(raw), "1 commit(s) on branch work")
		require.Contains(t, string(raw), "written-by: nova-swarm native")
	})

	t.Run("a result the card published is never overwritten", func(t *testing.T) {
		job := t.TempDir()
		aRepoWithACommitPastItsBase(t, filepath.Join(job, "repo"))
		require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("RESULT: x sha=1\n"), 0o644))

		_, wrote, err := WriteDeadlineResult(job, "card-1", true)
		require.NoError(t, err)
		require.False(t, wrote)
		raw, _ := os.ReadFile(filepath.Join(job, "RESULT.md"))
		require.Equal(t, "RESULT: x sha=1\n", string(raw))
	})

	t.Run("no commit, or not the deadline, writes nothing", func(t *testing.T) {
		job := t.TempDir()
		_, wrote, err := WriteDeadlineResult(job, "card-1", true)
		require.NoError(t, err)
		require.False(t, wrote, "a job with no repo has nothing to report")

		job2 := t.TempDir()
		aRepoWithACommitPastItsBase(t, filepath.Join(job2, "repo"))
		_, wrote, err = WriteDeadlineResult(job2, "card-1", false)
		require.NoError(t, err)
		require.False(t, wrote, "a run the deadline did not end is some other end")
	})
}

package friend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lane that has written its report, with that head on origin, leaves no job
// directory. The keep cap holds a finished job whose head is not confirmed; it
// does not hold one whose report names a head origin has. The branch in the
// mirror is the record (2026-10-06: job directories were left until the disk
// filled, and nothing removed one when its lane ended).
func TestAFinishedLaneLeavesNoJobDirectory(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	finished, ok := PacketOf(stagedCard("done.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	stay, ok := PacketOf(stagedCard("kept.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	for _, p := range []Packet{finished, stay} {
		_, err := stager.Stage(context.Background(), p)
		require.NoError(t, err)
	}
	checkout := filepath.Join(JobDir(dir, finished.Job), "repo")
	g.Commit(checkout, map[string]string{"work.md": "the work\n"})
	work := gitIn(t, env, checkout, "rev-parse", "HEAD")
	gitIn(t, env, checkout, "push", "-q", "origin", finished.Branch)

	report := filepath.Join(dir, "outbox", finished.Job, "REPORT.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(report), 0o755))
	require.NoError(t, os.WriteFile(report, []byte("Verdict: LAND\nHead: "+work+"\n\nthe lane finished\n"), 0o644))
	// both cards have left her row: no brief, no lane. The keep cap would
	// have held both. The one whose head is on origin must still go.
	pruned, err := stager.Prune(context.Background(), nil, FinishedJobsKept)
	require.NoError(t, err)
	assert.Contains(t, pruned, finished.Job)
	assert.NoDirExists(t, JobDir(dir, finished.Job), "a finished lane leaves no job directory")
	assert.DirExists(t, filepath.Join(JobDir(dir, stay.Job), "repo"), "a job whose head is not on origin stays inside the keep cap")
	mirror := filepath.Join(dir, MirrorsDir, "mas-bandwidth", "nova-tools.git")
	assert.Equal(t, work, gitIn(t, env, mirror, "rev-parse", "refs/heads/"+finished.Branch), "the branch stays in the mirror")
}

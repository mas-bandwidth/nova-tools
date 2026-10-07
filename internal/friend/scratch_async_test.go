package friend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsyncCleanupReservesScratchBeforeReturning(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	s := &Stager{Dir: t.TempDir(), Env: env, URL: func(string) string { return g.Remote }}
	dead, _ := PacketOf(stagedCard("dead.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	active, _ := PacketOf(stagedCard("active.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	for _, p := range []Packet{dead, active} {
		_, err := s.Stage(context.Background(), p)
		require.NoError(t, err)
	}
	jobs, err := s.PruneAsync(context.Background(), map[string]bool{active.Job: true}, 0)
	require.NoError(t, err)
	assert.Empty(t, jobs)
	require.True(t, s.CleanupPending(dead.Job), "receipt is not consumed until a later tick")
	assert.False(t, s.CleanupPending(active.Job))
	d := &Daemon{Dir: s.Dir, Stage: s.Stage, CleanupPending: s.CleanupPending}
	assert.True(t, d.stageOwed(stagedCard("dead.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical")), "an old JOB cannot hand a reserved checkout to a new lane")
	_, err = s.Stage(context.Background(), dead)
	require.ErrorContains(t, err, "cleanup")
	// Wait on the actual worker receipt, not on time or a polling sleep.
	s.cleanupMu.Lock()
	pass := s.cleanup
	s.cleanupMu.Unlock()
	result := <-pass.done
	pass.done <- result
	jobs, err = s.PruneAsync(context.Background(), map[string]bool{active.Job: true}, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{dead.Job}, jobs)
	assert.NoDirExists(t, JobDir(s.Dir, dead.Job))
	assert.DirExists(t, JobDir(s.Dir, active.Job))
	assert.False(t, s.CleanupPending(dead.Job))
	_, err = s.Stage(context.Background(), dead)
	require.NoError(t, err, "retained mirror branch can reconstruct after cleanup")
}

func TestFriendsAndReadersShareOneMachineMirror(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	root := t.TempDir()
	mirrorRoot := filepath.Join(root, "machine", "mirrors")
	a := &Stager{Dir: filepath.Join(root, "ada"), MirrorRoot: mirrorRoot, Env: env, URL: func(string) string { return g.Remote }}
	b := &Stager{Dir: filepath.Join(root, "bob"), MirrorRoot: mirrorRoot, Env: env, URL: func(string) string { return g.Remote }}
	pa, _ := PacketOf(stagedCard("a.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	pb, _ := PacketOf(stagedCard("b.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	head, err := a.Stage(context.Background(), pa)
	require.NoError(t, err)
	_, err = b.Stage(context.Background(), pb)
	require.NoError(t, err)
	for _, s := range []*Stager{a, b} {
		assert.NoDirExists(t, filepath.Join(s.Dir, MirrorsDir))
	}
	mirror := filepath.Join(mirrorRoot, "mas-bandwidth", "nova-tools.git")
	assert.Len(t, worktreesOf(t, env, mirror), 2)
	// Separate Stagers contend on the same kernel lock, not just their private mutexes.
	held, err := a.lockMirror(pa.Repo, true)
	require.NoError(t, err)
	_, err = b.lockMirror(pb.Repo, true)
	require.Error(t, err)
	require.NoError(t, held.Unlock())
	gitIn(t, env, filepath.Join(JobDir(a.Dir, pa.Job), "repo"), "push", "-q", "origin", pa.Branch)
	read := AskedRead{ID: "read.w1", Packet: ReadPacket{Head: head, WorkBranch: pa.Branch, Brief: "REPO: mas-bandwidth/nova-tools\nBASE: sprint/mechanical\n"}}
	require.NoError(t, b.StageRead(context.Background(), read))
	checkout := filepath.Join(b.Dir, "reads", read.ID, "repo")
	assert.Equal(t, head, gitIn(t, env, checkout, "rev-parse", "HEAD"))
	assert.NoFileExists(t, filepath.Join(checkout, ".git", "objects", "info", "alternates"))
	linked := false
	require.NoError(t, filepath.Walk(filepath.Join(mirror, "objects"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(filepath.Join(mirror, "objects"), path)
		other, e := os.Stat(filepath.Join(checkout, ".git", "objects", rel))
		if e == nil && os.SameFile(info, other) {
			linked = true
		}
		return nil
	}))
	assert.True(t, linked, "reader uses hardlinked mirror objects, not another physical full clone")
	require.NoError(t, os.WriteFile(filepath.Join(b.Dir, "reads", read.ID, "RESULT.md"), []byte("verdict: ok\n"), 0600))
	l := &loop{d: &Daemon{Dir: b.Dir, ReleaseRead: b.ReleaseRead, Record: func(string) {}}, reads: &readSet{running: map[string]bool{}}}
	l.sweepReads(`{"cards":[]}`, time.Unix(0, 0))
	assert.NoDirExists(t, checkout, "a killed read absent from a successful queue is swept")
	assert.FileExists(t, filepath.Join(b.Dir, "reads", read.ID, "RESULT.md"))
}

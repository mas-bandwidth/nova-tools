package friend

import (
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Removing the live map or inbox entry must not remove running, unowned or unpublished work.
func TestCleanupRequiresOwnedPublishedCompletion(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"running", "unpublished", "dirty", "untracked", "ignored", "later-commit", "wrong-origin-branch", "no-receipt", "no-report", "preexisting-job", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			g := testkit.Git(t, 1)
			g.Commit(g.Clones[0], map[string]string{"README.md": "base\n", ".gitignore": "ignored.txt\n"})
			env := stageEnv(t)
			gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/base")
			dir := t.TempDir()
			s := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
			p, ok := PacketOf(stagedCard("safe.w1", "working", "o/r", "base"))
			require.True(t, ok)
			if kind == "preexisting-job" {
				require.NoError(t, os.MkdirAll(JobDir(dir, p.Job), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(JobDir(dir, p.Job), "keep.txt"), []byte("another creator"), 0644))
			}
			_, err := s.Stage(context.Background(), p)
			require.NoError(t, err)
			checkout := filepath.Join(JobDir(dir, p.Job), "repo")
			g.Commit(checkout, map[string]string{"work.md": "work\n"})
			head := gitIn(t, env, checkout, "rev-parse", "HEAD")
			if kind != "unpublished" {
				gitIn(t, env, checkout, "push", "-q", "origin", p.Branch)
			}
			report := filepath.Join(dir, "outbox", p.Job, "REPORT.md")
			require.NoError(t, os.MkdirAll(filepath.Dir(report), 0755))
			require.NoError(t, os.WriteFile(report, []byte("Verdict: HOLD\nHead: "+head+"\n"), 0644))
			switch kind {
			case "running":
				require.NoError(t, os.WriteFile(laneMarkPath(dir, p.Job), []byte(LaneMarkRunning("a live lane", time.Unix(1, 0))), 0644))
			case "dirty":
				require.NoError(t, os.WriteFile(filepath.Join(checkout, "work.md"), []byte("pending"), 0644))
			case "untracked":
				require.NoError(t, os.WriteFile(filepath.Join(checkout, "pending.txt"), []byte("pending"), 0644))
			case "ignored":
				require.NoError(t, os.WriteFile(filepath.Join(checkout, "ignored.txt"), []byte("pending"), 0644))
			case "later-commit":
				g.Commit(checkout, map[string]string{"later.md": "pending"})
			case "wrong-origin-branch":
				gitIn(t, env, checkout, "push", "-q", "origin", ":"+p.Branch)
			case "no-receipt":
				require.NoError(t, os.Remove(filepath.Join(JobDir(dir, p.Job), scratchReceipt)))
			case "no-report":
				require.NoError(t, os.Remove(report))
			case "symlink":
				outside := filepath.Join(t.TempDir(), "repo")
				require.NoError(t, os.Rename(checkout, outside))
				require.NoError(t, os.Symlink(outside, checkout))
			}
			pruned, err := s.Prune(context.Background(), nil, 0)
			if kind != "symlink" {
				require.NoError(t, err)
			}
			assert.Empty(t, pruned)
			assert.DirExists(t, JobDir(dir, p.Job), "loss of server presence must preserve scratch")
		})
	}
}

func TestReaderScratchSharesTheMirrorAndLeavesAnArchivedFinding(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "base\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/base")
	head := gitIn(t, env, g.Clones[0], "rev-parse", "HEAD")
	dir := t.TempDir()
	s := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	read := AskedRead{ID: "read.w1", Packet: ReadPacket{Brief: "REPO: o/r\nBASE: base\n", Head: head, WorkBranch: "base"}}
	path := filepath.Join(dir, "reads", read.ID)
	require.NoError(t, os.MkdirAll(path, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(path, scratchReceipt), []byte(read.ID), 0644))
	require.NoError(t, s.StageRead(context.Background(), read))
	mirror := filepath.Join(dir, MirrorsDir, "o", "r.git")
	assert.Len(t, worktreesOf(t, env, mirror), 1)
	for _, name := range []string{"BRIEF.md", "READ.md", "WORKER-REPORT.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(name), 0644))
	}
	l := &loop{ctx: context.Background(), d: &Daemon{Dir: dir, ReadRelease: s.ReleaseRead, Record: func(string) {}}}
	l.releaseRead(readResult{read: read, dir: path}, []byte("the recorded finding"), time.Unix(1, 0))
	assert.NoDirExists(t, path)
	raw, err := os.ReadFile(filepath.Join(dir, "outbox", "reads", read.ID, "RESULT.md"))
	require.NoError(t, err)
	assert.Equal(t, "the recorded finding", string(raw))
	assert.Empty(t, worktreesOf(t, env, mirror))
	assert.DirExists(t, mirror)
}

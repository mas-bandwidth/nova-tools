package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreesOf is the checkouts a mirror's `git worktree list` names, the bare mirror itself
// left out. The mirror is the record git marks "bare", never matched by path: git names it by
// its real path, which is not the path the test made it at when the temporary directory is
// under a symlink (macOS: /var is /private/var).
func worktreesOf(t *testing.T, env []string, mirror string) []string {
	var out []string
	for _, rec := range strings.Split(gitIn(t, env, mirror, "worktree", "list", "--porcelain"), "\n\n") {
		var path string
		bare := false
		for _, l := range strings.Split(rec, "\n") {
			if p, ok := strings.CutPrefix(l, "worktree "); ok {
				path = p
			}
			bare = bare || l == "bare"
		}
		if path != "" && !bare {
			out = append(out, path)
		}
	}
	return out
}

// On 2026-10-05 her disk reached 99% with 42 staged clones and 765 finished job dirs: every
// job was a whole clone. A job is now a git worktree of the repository's one full bare
// mirror, at the card's base on the card's branch: two jobs of one repository are one mirror
// and two worktrees, a push from a worktree goes to the repository and is read as the job's
// head, a fetch that fails is named on the job it was staging, and a finished job's worktree
// is pruned (its branch, with any commit on it, kept in the mirror), past a cap.
func TestJobsAreWorktreesOfOneMirror(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	base := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	a, ok := PacketOf(stagedCard("a.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	b, ok := PacketOf(stagedCard("b.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	for _, p := range []Packet{a, b} {
		sha, err := stager.Stage(context.Background(), p)
		require.NoError(t, err, "%s", p.Job)
		assert.Equal(t, base, sha)
	}

	mirrors, err := filepath.Glob(filepath.Join(dir, MirrorsDir, "*", "*"))
	require.NoError(t, err)
	mirror := filepath.Join(dir, MirrorsDir, "mas-bandwidth", "nova-tools.git")
	require.Equal(t, []string{mirror}, mirrors, "one mirror for the repository")
	assert.Equal(t, "true", gitIn(t, env, mirror, "rev-parse", "--is-bare-repository"), "the mirror is bare")
	assert.Equal(t, "false", gitIn(t, env, mirror, "rev-parse", "--is-shallow-repository"), "and full: never shallow")
	assert.NotContains(t, gitIn(t, env, mirror, "rev-list", "--objects", "--missing=print", "--all"), "?", "and never blob-less: no object missing")
	assert.Equal(t, base, gitIn(t, env, mirror, "rev-parse", "refs/remotes/origin/sprint/mechanical"), "origin's branches are its remote-tracking refs")

	var checkouts []string
	for _, p := range []Packet{a, b} {
		checkout := filepath.Join(JobDir(dir, p.Job), "repo")
		checkouts = append(checkouts, checkout)
		fi, err := os.Lstat(filepath.Join(checkout, ".git"))
		require.NoError(t, err)
		assert.False(t, fi.IsDir(), "%s is a worktree: its .git is a file, its objects the mirror's", p.Job)
		common := gitIn(t, env, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
		assert.Equal(t, evalPath(t, mirror), evalPath(t, common), "%s is a worktree of the one mirror", p.Job)
		assert.Equal(t, base, gitIn(t, env, checkout, "rev-parse", "HEAD"), "at the base")
		assert.Equal(t, p.Branch, gitIn(t, env, checkout, "rev-parse", "--abbrev-ref", "HEAD"), "on the card's branch")
		assert.Equal(t, g.Remote, gitIn(t, env, checkout, "remote", "get-url", "origin"), "origin is the repository, never the mirror")
		assert.Equal(t, base, gitIn(t, env, checkout, "rev-parse", "origin/sprint/mechanical"))
		assert.FileExists(t, filepath.Join(checkout, "README.md"), "checked out")
		assert.FileExists(t, filepath.Join(JobDir(dir, p.Job), JobFile))
		assert.NoDirExists(t, filepath.Join(JobDir(dir, p.Job), stageScratch), "no scratch left behind")
	}
	assert.ElementsMatch(t, evalPaths(t, checkouts), evalPaths(t, worktreesOf(t, env, mirror)), "two worktrees of the one mirror")

	// a commit pushed from a worktree goes to the repository, and is read as the job's head
	ca := checkouts[0]
	g.Commit(ca, map[string]string{"work.md": "the work\n"})
	work := gitIn(t, env, ca, "rev-parse", "HEAD")
	gitIn(t, env, ca, "push", "-q", "origin", a.Branch)
	assert.Equal(t, work, gitIn(t, env, g.Remote, "rev-parse", "refs/heads/"+a.Branch), "pushed to the repository")
	brief := filepath.Join(dir, "inbox", a.Job, "BRIEF.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(brief), 0o755))
	require.NoError(t, os.WriteFile(brief, []byte(stagedCard("a.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical").Brief), 0o644))
	head, branch := PushedHead(dir, Card{ID: "a.w1", Brief: brief, Outbox: filepath.Join(dir, "outbox", a.Job)})
	assert.Equal(t, a.Branch, branch)
	assert.Equal(t, work, head, "the push is read through the worktree's .git file")

	// the next fetch (a fresh stager: no fetch is fresh) fails: it is her account not reaching
	// the repository, named on the job it was staging, and nothing of that job is there
	broken := &Stager{Dir: dir, Env: env, URL: func(string) string { return filepath.Join(t.TempDir(), "gone.git") }}
	c, ok := PacketOf(stagedCard("c.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	_, err = broken.Stage(context.Background(), c)
	var ns *NotStageable
	require.ErrorAs(t, err, &ns, "a fetch that fails is a judgment")
	assert.Equal(t, c.Job, ns.Job)
	assert.Contains(t, err.Error(), "staging jobs/"+c.Job, "named on the job")
	assert.Contains(t, ns.Why, "git could not fetch")
	assert.NoDirExists(t, filepath.Join(JobDir(dir, c.Job), "repo"))
	assert.NoFileExists(t, filepath.Join(JobDir(dir, c.Job), JobFile))
	assert.Len(t, worktreesOf(t, env, mirror), 2, "no worktree for the job that failed")

	// finished jobs (no brief in her inbox, no lane, no stage) are pruned past the cap, oldest
	// first; a live job and one whose brief is still in her inbox are never pruned
	d, ok := PacketOf(stagedCard("d.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	_, err = stager.Stage(context.Background(), d)
	require.NoError(t, err)
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(JobDir(dir, a.Job), JobFile), old, old))
	require.NoError(t, os.RemoveAll(filepath.Dir(brief))) // a's card left her row: the inbox cleanup retired inbox/<job>
	pruned, err := stager.Prune(context.Background(), map[string]bool{b.Job: true}, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{a.Job}, pruned, "b is live; d is the one finished job the cap keeps")
	assert.NoDirExists(t, JobDir(dir, a.Job), "a finished job is gone whole")
	assert.DirExists(t, filepath.Join(JobDir(dir, b.Job), "repo"))
	assert.DirExists(t, filepath.Join(JobDir(dir, d.Job), "repo"))
	assert.Len(t, worktreesOf(t, env, mirror), 2, "its worktree is pruned from the mirror")
	assert.Equal(t, work, gitIn(t, env, mirror, "rev-parse", "refs/heads/"+a.Branch), "its branch, and its commit, stay in the mirror")

	// a job in her inbox is never finished, and a cap of none prunes every finished one
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", b.Job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", b.Job, "BRIEF.md"), []byte("STATUS: nova-sprint card b.w1\n"), 0o644))
	pruned, err = stager.Prune(context.Background(), nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{d.Job}, pruned)
	assert.Equal(t, evalPaths(t, checkouts[1:2]), evalPaths(t, worktreesOf(t, env, mirror)))

	// a pruned job staged again takes its branch back from the mirror, its work on it
	sha, err := stager.Stage(context.Background(), a)
	require.NoError(t, err)
	assert.Equal(t, work, sha, "the commit the checkout is at")
	assert.Equal(t, work, gitIn(t, env, ca, "rev-parse", "HEAD"), "the branch the job left, never a reset to the base")
}

func evalPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err, p)
	return r
}

func evalPaths(t *testing.T, ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, evalPath(t, p))
	}
	return out
}

// The cleanup the daemon already owns prunes finished jobs: after each inbox reconcile (which
// retires the briefs of cards that left her row) it hands Prune the jobs that are live, held
// on her row or run by a lane, says each job removed, and says a failure once while it stands.
func TestTheInboxCleanupPrunesFinishedJobs(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	row := &twinRow{}
	r.d.Held = row.held
	held := workCard("held.w1", "working")
	row.set(held)
	var lives []map[string]bool
	fail := true
	r.d.Prune = func(_ context.Context, live map[string]bool) ([]string, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		lives = append(lives, live)
		if fail {
			return []string{"old.w1~15"}, fmt.Errorf("jobs/stuck.w1~15: worktree locked")
		}
		return nil, nil
	}
	r.run(t, 1)
	r.run(t, 1) // a second cleanup, the failure standing
	r.mu.Lock()
	require.Len(t, lives, 2, "a prune after each inbox cleanup")
	assert.True(t, lives[0][held.Job], "a held job is live")
	r.mu.Unlock()
	var removed, failed []string
	for _, l := range r.records {
		switch {
		case strings.Contains(l, " prune: removed "):
			removed = append(removed, l)
		case strings.Contains(l, " prune: "):
			failed = append(failed, l)
		}
	}
	require.Len(t, removed, 2, "each job removed is said: %v", removed)
	assert.Contains(t, removed[0], "prune: removed jobs/old.w1~15 and its worktree: its card is finished")
	require.Len(t, failed, 1, "a failure is said once while it stands: %v", failed)
	assert.Contains(t, failed[0], "prune: not pruned: jobs/stuck.w1~15: worktree locked")

	r.mu.Lock()
	fail = false
	r.mu.Unlock()
	row.set()
	r.run(t, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Len(t, lives, 3, "an empty row prunes too")
	assert.False(t, lives[len(lives)-1][held.Job], "a job off her row is no longer live")
}

// A mirror made before jobs were worktrees held origin's branches as its own: its first fetch
// converts it in place, origin's branches deleted as its own and fetched back as remote-tracking
// refs, so a job's branch never meets a copy of origin's.
func TestAMirrorOfTheCloneLayoutIsConverted(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	base := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")
	dir := t.TempDir()
	mirror := filepath.Join(dir, MirrorsDir, "o", "r.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(mirror), 0o755))
	gitIn(t, env, "", "clone", "--quiet", "--bare", "--", g.Remote, mirror)
	gitIn(t, env, mirror, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	gitIn(t, env, mirror, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
	require.Equal(t, base, gitIn(t, env, mirror, "rev-parse", "refs/heads/sprint/mechanical"), "the old layout")

	p, ok := PacketOf(stagedCard("a.w1", "working", "o/r", "sprint/mechanical"))
	require.True(t, ok)
	sha, err := (&Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}).Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	assert.Equal(t, strings.Join(mirrorFetch, "\n"), gitIn(t, env, mirror, "config", "--get-all", "remote.origin.fetch"))
	assert.Equal(t, "refs/heads/"+p.Branch, gitIn(t, env, mirror, "for-each-ref", "--format=%(refname)", "refs/heads/"), "the job's branch is the mirror's only one")
	assert.Equal(t, base, gitIn(t, env, mirror, "rev-parse", "refs/remotes/origin/sprint/mechanical"))
	assert.Equal(t, p.Branch, gitIn(t, env, filepath.Join(JobDir(dir, p.Job), "repo"), "rev-parse", "--abbrev-ref", "HEAD"))
}

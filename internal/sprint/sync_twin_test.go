package sprint_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dev sync on the twin store and a twin repository (docs/SPEC-SPRINT.md, "Dev sync
// every cycle"): a bare origin with main, dev and the base; a developer's clone that
// commits to dev and the base; and the land round's own clone, where the sync runs.

const syncBase = "sprint/mechanical-2026-10-02"

type syncRepo struct {
	t                 *testing.T
	origin, dev, land string
	env               []string
}

func newSyncRepo(t *testing.T) *syncRepo {
	t.Helper()
	root := t.TempDir()
	r := &syncRepo{t: t, origin: filepath.Join(root, "origin.git"), dev: filepath.Join(root, "dev"), land: filepath.Join(root, "land"),
		env: testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")}
	r.syncGit(root, "init", "-q", "--bare", "-b", "main", r.origin)
	r.syncGit(root, "clone", "-q", r.origin, r.dev)
	require.NoError(t, os.WriteFile(filepath.Join(r.dev, "README.md"), []byte("# repo\n"), 0o644))
	r.syncGit(r.dev, "add", "README.md")
	r.syncGit(r.dev, "commit", "-q", "-m", "initial commit")
	r.syncGit(r.dev, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/dev", "HEAD:refs/heads/"+syncBase)
	r.syncGit(root, "clone", "-q", r.origin, r.land)
	return r
}

func (r *syncRepo) syncGit(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// commit is a developer's commit of file on branch, pushed.
func (r *syncRepo) commit(branch, file, content, msg string) {
	r.t.Helper()
	r.syncGit(r.dev, "fetch", "-q", "origin")
	r.syncGit(r.dev, "checkout", "-q", "-B", branch, "origin/"+branch)
	require.NoError(r.t, os.WriteFile(filepath.Join(r.dev, file), []byte(content), 0o644))
	r.syncGit(r.dev, "add", file)
	r.syncGit(r.dev, "commit", "-q", "-m", msg)
	r.syncGit(r.dev, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
}

func (r *syncRepo) tip(branch string) string { return r.syncGit(r.origin, "rev-parse", branch) }

func (r *syncRepo) has(branch, rev string) bool {
	cmd := exec.CommandContext(r.t.Context(), "git", "merge-base", "--is-ancestor", rev, branch)
	cmd.Dir, cmd.Env = r.origin, r.env
	return cmd.Run() == nil
}

// syncRig is the twin store with streams s1 and s2, and the twin repository.
type syncRig struct {
	t    *testing.T
	ctx  context.Context
	st   *store.Store
	repo *syncRepo
	mu   sync.Mutex
	now  time.Time
	gate []string // the trees the tree gate saw: the README of each
	red  error
}

func newSyncRig(t *testing.T) *syncRig {
	t.Helper()
	r := &syncRig{t: t, ctx: t.Context(), repo: newSyncRepo(t), now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	m := store.NewMem()
	n := 0
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	for _, s := range []string{"s1", "s2"} {
		res, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: s, Count: 1}))
		require.NoError(t, err)
		require.Empty(t, res.Refused)
	}
	return r
}

func (r *syncRig) tick(d time.Duration) { r.mu.Lock(); r.now = r.now.Add(d); r.mu.Unlock() }

func (r *syncRig) load() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(r.t, err)
	return s
}

// cycle is one land cycle's dev sync as the land round runs it: LandCycleSync on the
// snapshot the round read, in its clone through its tree gate, then the facts recorded in
// one step on the store.
func (r *syncRig) cycle() (sprint.DevSyncFacts, bool, error) {
	r.t.Helper()
	req := sprint.DevSyncReq{RepoDir: r.repo.land, Base: syncBase, Env: r.repo.env,
		Check: func(_ context.Context, dir string) error {
			b, err := os.ReadFile(filepath.Join(dir, "feature.go"))
			if err != nil {
				return err
			}
			r.gate = append(r.gate, string(b))
			return r.red
		}}
	f, due, err := sprint.LandCycleSync(r.ctx, r.load(), req)
	if !due || err != nil {
		return f, due, err
	}
	res, err := r.st.Run(r.ctx, store.Step{Verb: "dev-sync", Load: []string{sprint.Work, sprint.Merge},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.DevSynced(s, f, req.Streams) }})
	require.NoError(r.t, err)
	require.Empty(r.t, res.Refused)
	return f, true, nil
}

func (r *syncRig) open() []sprint.Open {
	var out []sprint.Open
	for _, o := range r.load().Open {
		if o.Note.Type == sprint.NDevSyncConflict {
			out = append(out, o)
		}
	}
	return out
}

func TestTheBaseTakesTheDevelopmentBranchEveryCycle(t *testing.T) {
	t.Parallel()
	r := newSyncRig(t)
	repo := r.repo

	// dev moves ahead of the base by two commits; the first cycle (no sync recorded) merges
	// it through the tree gate and pushes the merge onto the base, like a batch
	repo.commit("dev", "feature.go", "package f // one\n", "dev commit 1")
	repo.commit("dev", "other.go", "package f\n", "dev commit 2")
	devTip := repo.tip("dev")
	f, due, err := r.cycle()
	require.NoError(t, err)
	require.True(t, due, "no dev sync recorded: the cycle syncs")
	assert.True(t, f.Synced)
	assert.Equal(t, 2, f.Drift.BaseLacks)
	assert.Equal(t, []string{"package f // one\n"}, r.gate, "the merged tree went through the tree gate")
	assert.True(t, repo.has(syncBase, devTip), "the base on origin holds dev")
	assert.Equal(t, f.MergeSha, repo.tip(syncBase))
	s := r.load()
	d := sprint.DevDriftOf(s)
	assert.Equal(t, 0, d.BaseLacks)
	assert.Equal(t, f.MergeSha, d.LastSha)
	assert.Equal(t, "0", s.StreamCtl("s1").F(sprint.FieldDevSyncBaseLacks), "the drift is on the merge row")

	// the next cycle at the same clock, nothing landed: not due, no git
	repo.commit("dev", "feature.go", "package f // two\n", "dev commit 3")
	_, due, err = r.cycle()
	require.NoError(t, err)
	assert.False(t, due)
	assert.Equal(t, f.MergeSha, repo.tip(syncBase))

	// a red tree gate pushes nothing and records nothing
	r.tick(sprint.DevSyncAge)
	r.red = errors.New("vet: red")
	_, due, err = r.cycle()
	require.ErrorContains(t, err, "vet: red")
	assert.True(t, due)
	assert.Equal(t, f.MergeSha, repo.tip(syncBase), "a red gate pushes nothing")
	assert.Empty(t, repo.syncGit(repo.land, "status", "--porcelain"), "the clone is clean")
	r.red = nil

	// the base and dev both change one file: a conflict stops every stream with ONE
	// judgment naming the file, and pushes nothing
	repo.commit(syncBase, "feature.go", "package f // base\n", "base edit")
	baseTip := repo.tip(syncBase)
	f, due, err = r.cycle()
	require.NoError(t, err)
	require.True(t, due)
	assert.True(t, f.Conflict)
	assert.Equal(t, []string{"feature.go"}, f.Files)
	assert.Equal(t, baseTip, repo.tip(syncBase))
	s = r.load()
	for _, st := range []string{"s1", "s2"} {
		assert.Equal(t, sprint.StreamStopped, s.StreamCtl(st).F("state"), st)
		assert.Equal(t, sprint.DevSyncCause, s.StreamCtl(st).F("cause"), st)
	}
	js := r.open()
	require.Len(t, js, 1, "ONE judgment")
	assert.Contains(t, js[0].Note.What, "feature.go")

	// every cycle tries again while it is open: still one judgment
	r.tick(time.Minute)
	_, due, err = r.cycle()
	require.NoError(t, err)
	require.True(t, due, "an open conflict makes every cycle due")
	assert.Len(t, r.open(), 1)

	// merged by hand: the next cycle finds the base holds dev, closes the judgment and
	// resumes both streams
	repo.syncGit(repo.dev, "fetch", "-q", "origin")
	repo.syncGit(repo.dev, "checkout", "-q", "-B", syncBase, "origin/"+syncBase)
	cmd := exec.CommandContext(t.Context(), "git", "merge", "-q", "origin/dev")
	cmd.Dir, cmd.Env = repo.dev, repo.env
	require.Error(t, cmd.Run(), "the hand merge conflicts as the cycle's did")
	require.NoError(t, os.WriteFile(filepath.Join(repo.dev, "feature.go"), []byte("package f // both\n"), 0o644))
	repo.syncGit(repo.dev, "commit", "-q", "-am", "merge dev by hand")
	repo.syncGit(repo.dev, "push", "-q", "origin", "HEAD:refs/heads/"+syncBase)
	r.tick(time.Minute)
	f, due, err = r.cycle()
	require.NoError(t, err)
	require.True(t, due)
	assert.False(t, f.Conflict)
	assert.Empty(t, r.open(), "the judgment is closed")
	s = r.load()
	for _, st := range []string{"s1", "s2"} {
		assert.Equal(t, sprint.StreamMerging, s.StreamCtl(st).F("state"), st)
	}
	assert.Equal(t, repo.tip(syncBase), sprint.DevDriftOf(s).LastSha)
}

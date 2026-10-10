package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deliverRepo is a repository with sprint/mechanical, and git's environment for a stager.
func deliverRepo(t *testing.T) (remote, base string, env []string) {
	t.Helper()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env = stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	return g.Remote, gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical"), env
}

// The daemon stages a card's job before it writes the card's brief: a runner starts a lane
// within seconds of BRIEF.md, and one that met no checkout held the card ("the staged checkout
// is missing"). At the moment each stage ends no BRIEF.md is there; once it is, jobs/<job>/repo
// and JOB.md are there before it, by the files' times and by the record's order; a card her
// account cannot reach has no brief written at all, so no runner meets it.
func TestTheDaemonStagesBeforeItWritesTheBrief(t *testing.T) {
	t.Parallel()
	remote, base, env := deliverRepo(t)
	r := newRig(t)
	r.d.Coordinator = "ada"
	dir := r.d.Dir
	urls := map[string]string{"mas-bandwidth/nova-tools": remote, "mas-bandwidth/private": filepath.Join(t.TempDir(), "no-such-repo")}
	stager := &Stager{Dir: dir, Mirrors: filepath.Join(t.TempDir(), "state", MirrorsDir), Env: env, URL: func(repo string) string { return urls[repo] }}
	var mu sync.Mutex
	var briefAtStage []string // a job whose BRIEF.md was there when its stage ended
	ended := make(chan struct{}, 8)
	r.d.Stage = func(ctx context.Context, p Packet) (string, error) {
		defer func() { ended <- struct{}{} }()
		sha, err := stager.Stage(ctx, p)
		if exists(filepath.Join(dir, "inbox", p.Job, "BRIEF.md")) {
			mu.Lock()
			briefAtStage = append(briefAtStage, p.Job)
			mu.Unlock()
		}
		return sha, err
	}
	row := &twinRow{}
	r.d.Held = row.held
	cards := []HeldCard{
		stagedCard("first.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"),
		stagedCard("second.w1", "ready", "mas-bandwidth/nova-tools", "sprint/mechanical"),
	}
	private := stagedCard("private.w1", "working", "mas-bandwidth/private", "main")
	row.set(append(slices.Clone(cards), private)...)

	// the rig's stop ends a stage under way: the first step waits for the three it started
	r.at[1] = func() {
		for range 3 {
			<-ended
		}
	}
	r.run(t, 1) // stages, then writes, on the stage's goroutine; Run waits for it
	r.run(t, 1) // says what it did
	mu.Lock()
	assert.Empty(t, briefAtStage, "no brief is written before its job's stage ends")
	mu.Unlock()
	for _, h := range cards {
		job := JobDir(dir, h.Job)
		brief := filepath.Join(dir, "inbox", h.Job, "BRIEF.md")
		require.FileExists(t, brief, "%s is delivered", h.Card)
		assert.Equal(t, base, gitIn(t, env, filepath.Join(job, "repo"), "rev-parse", "HEAD"), "%s's checkout is at its base", h.Card)
		jobAt, err := os.Stat(filepath.Join(job, JobFile))
		require.NoError(t, err)
		briefAt, err := os.Stat(brief)
		require.NoError(t, err)
		assert.False(t, briefAt.ModTime().Before(jobAt.ModTime()), "%s: JOB.md (%s) is written before BRIEF.md (%s)", h.Card, jobAt.ModTime(), briefAt.ModTime())
		assert.NoFileExists(t, filepath.Join(job, StageMark), "the stage's mark goes once JOB.md is there")
		assert.NoFileExists(t, stageLock(dir, h.Job), "and its lock file")
		staged, wrote := -1, -1
		for i, l := range r.records {
			switch {
			case strings.Contains(l, " stage: staged jobs/"+h.Job+"/repo "):
				staged = i
			case strings.Contains(l, " inbox: wrote inbox/"+h.Job+"/BRIEF.md "):
				wrote = i
			}
		}
		require.NotEqual(t, -1, staged, "the stage is said: %v", r.records)
		assert.Greater(t, wrote, staged, "%s: the record says the stage before the brief", h.Card)
	}
	assert.DirExists(t, MirrorDir(stager.Mirrors, "mas-bandwidth/nova-tools"), "the mirror is kept where the setting names")
	assert.NoDirExists(t, filepath.Join(dir, MirrorsDir), "and nowhere else")
	assert.NoFileExists(t, filepath.Join(dir, "inbox", private.Job, "BRIEF.md"), "a card whose job cannot be staged has no brief written")
}

// Every card on her row is delivered whatever its WHO line prefers: the deal placed it there,
// and a pin is a preference, never a reason not to write it.
func TestACardPinnedToAnotherFriendOnHerRowIsDelivered(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	row := &twinRow{}
	r.d.Held = row.held
	pinned := workCard("pinned.w1", "working")
	pinned.Brief = strings.Replace(pinned.Brief, "\n\n", "\nWHO: friend carol\n\n", 1)
	only := workCard("only.w1", "ready")
	only.Brief = strings.Replace(only.Brief, "\n\n", "\nWHO: only friend carol\n\n", 1)
	row.set(pinned, only)
	r.run(t, 2)
	for _, h := range []HeldCard{pinned, only} {
		assert.Equal(t, h.Brief, briefOf(t, r.d.Dir, h.Job), "%s is bob's to run", h.Card)
	}
	o := Delivery{Dir: t.TempDir()}.One(context.Background(), pinned)
	assert.Equal(t, "DELIVER pinned.w1~15 brief", o.Line(), "the hand version writes it too")
}

// A friend whose runner stages its own jobs is handed the brief alone, and a second daemon run
// beside that runner (one that stages) never stages a job the runner started: the runner's
// directory, made with no JOB.md and no stage mark, is left as found, and the brief is written
// once the runner's JOB.md is there. Two stages of one job, a second daemon's beside the first,
// are kept apart by the job's lock.
func TestASecondDaemonBesideASelfStagingRunnerStagesNothingTwice(t *testing.T) {
	t.Parallel()
	remote, _, env := deliverRepo(t)
	r := newRig(t)
	dir := r.d.Dir
	h := stagedCard("own.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical")
	ctx := context.Background()

	// her own daemon, --stages runner: the brief alone, and no job directory made
	self := Delivery{Dir: dir}
	assert.False(t, self.Owed(h))
	assert.Equal(t, "DELIVER own.w1~15 brief", self.One(ctx, h).Line())
	assert.NoDirExists(t, filepath.Join(dir, JobsDir), "her runner stages its own jobs")

	// her runner reads the brief and starts its job: a directory, a clone under way, no JOB.md
	runnerFile := filepath.Join(JobDir(dir, h.Job), "repo", "half-cloned")
	require.NoError(t, os.MkdirAll(filepath.Dir(runnerFile), 0o755))
	require.NoError(t, os.WriteFile(runnerFile, []byte("the runner's\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(dir, "inbox", h.Job, "BRIEF.md")), "the brief is not there yet for the second daemon")

	// a second daemon beside it, one that stages, delivering the same row
	stages := 0
	stager := &Stager{Dir: dir, Mirrors: filepath.Join(t.TempDir(), MirrorsDir), Env: env, URL: func(string) string { return remote }}
	ended := make(chan struct{}, 8)
	r.d.Stage = func(ctx context.Context, p Packet) (string, error) {
		defer func() { ended <- struct{}{} }()
		r.mu.Lock()
		stages++
		r.mu.Unlock()
		return stager.Stage(ctx, p)
	}
	row := &twinRow{}
	r.d.Held = row.held
	row.set(h)
	r.at[1] = func() { <-ended }
	r.run(t, 1)
	r.at[1] = nil
	r.run(t, 1)
	r.mu.Lock()
	tried := stages
	r.mu.Unlock()
	assert.Positive(t, tried, "the second daemon tried it")
	assert.FileExists(t, runnerFile, "the runner's job is left as found")
	assert.NoFileExists(t, filepath.Join(JobDir(dir, h.Job), JobFile), "no JOB.md written over the runner's job")
	assert.NoFileExists(t, filepath.Join(JobDir(dir, h.Job), StageMark))
	assert.NoFileExists(t, filepath.Join(dir, "inbox", h.Job, "BRIEF.md"), "its brief waits for the runner's JOB.md")
	r.mu.Lock()
	said := strings.Join(r.records, "\n")
	r.mu.Unlock()
	assert.Contains(t, said, "stage: skipped jobs/own.w1~15: jobs/own.w1~15 was started elsewhere")
	assert.NotContains(t, said, "stage: staged")

	// the runner finishes its stage: the job is staged, and the brief is written alone
	require.NoError(t, os.WriteFile(filepath.Join(JobDir(dir, h.Job), JobFile), []byte("# JOB: the runner's\n"), 0o644))
	r.run(t, 1)
	assert.Equal(t, h.Brief, briefOf(t, dir, h.Job))
	r.mu.Lock()
	assert.Equal(t, tried, stages, "a staged job is never staged again")
	r.mu.Unlock()
	job, err := os.ReadFile(filepath.Join(JobDir(dir, h.Job), JobFile))
	require.NoError(t, err)
	assert.Equal(t, "# JOB: the runner's\n", string(job))

	// two daemons that both stage: the second meets the first's lock and stages nothing
	two := stagedCard("two.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical")
	p, ok := PacketOf(two)
	require.True(t, ok)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, JobsDir), 0o755))
	release, held, err := tryLock(stageLock(dir, p.Job))
	require.NoError(t, err)
	require.True(t, held)
	o := Delivery{Dir: dir, Stage: stager.Stage}.One(ctx, two)
	release()
	assert.Equal(t, DeliverSkipped, o.What)
	var away *StartedElsewhere
	if runtime.GOOS == "windows" {
		t.Skip("no flock on Windows: the mark alone keeps a second stage off")
	}
	require.ErrorAs(t, o.Err, &away, "the first stage holds the lock: %v", o.Err)
	assert.NoDirExists(t, JobDir(dir, p.Job), "nothing made")
	assert.NoFileExists(t, filepath.Join(dir, "inbox", two.Job, "BRIEF.md"))
	// a git another test forks in this process may hold a copy of the lock's file until its
	// exec closes it, so the lock is free within moments of its release, not at once
	require.Eventually(t, func() bool {
		o = Delivery{Dir: dir, Stage: stager.Stage}.One(ctx, two)
		return !errors.As(o.Err, &away)
	}, 10*time.Second, 10*time.Millisecond)
	assert.Equal(t, "DELIVER two.w1~15 staged", o.Line(), "staged once the lock is free")
}

// A stage that ended part way (its mark there, no JOB.md) is the stage's own and is resumed; a
// mirror that is a blob-less partial clone is refused naming its remedy, never cloned from.
func TestAStagesOwnPartJobIsResumedAndAPartialMirrorRefused(t *testing.T) {
	t.Parallel()
	remote, base, env := deliverRepo(t)
	dir := t.TempDir()
	h := stagedCard("part.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical")
	p, _ := PacketOf(h)
	require.NoError(t, os.MkdirAll(filepath.Join(JobDir(dir, p.Job), ".repo.staging"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(JobDir(dir, p.Job), StageMark), nil, 0o644))
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return remote }}
	sha, err := stager.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	assert.True(t, Staged(dir, p.Job))

	mirrors := t.TempDir()
	partial := MirrorDir(mirrors, "mas-bandwidth/nova-tools")
	gitIn(t, env, "", "clone", "-q", "--bare", "--filter=blob:none", "file://"+remote, partial)
	q := Packet{Card: "q.w1", Job: "q.w1~15", Repo: "mas-bandwidth/nova-tools", Base: "sprint/mechanical", Branch: "sprint/q.w1.g1.e15"}
	_, err = (&Stager{Dir: dir, Mirrors: mirrors, Env: env, URL: func(string) string { return remote }}).Stage(context.Background(), q)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a partial clone")
	assert.Contains(t, err.Error(), "pack has unresolved deltas")
	assert.NoDirExists(t, JobDir(dir, q.Job), "no job directory for a stage that cannot clone")
}

// The agent carries the daemon's delivery settings when they are set, and only then.
func TestTheAgentCarriesTheDeliverySettings(t *testing.T) {
	t.Parallel()
	a := Agent{Friend: "bob", Harness: "claude", Dir: "/w", Binary: "/bin/nova-friend", Redis: "r:1", Server: "s:2", Width: 2}
	assert.NotContains(t, a.Args(), "--stages")
	assert.NotContains(t, a.Args(), "--mirrors")
	a.Stages, a.Mirrors = "runner", "/m"
	args := strings.Join(a.Args(), " ")
	assert.Contains(t, args, "--stages runner")
	assert.Contains(t, args, "--mirrors /m")
}

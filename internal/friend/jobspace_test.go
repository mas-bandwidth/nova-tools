package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheDaemonKeepsJobsUnderTheCapAndReportsSpace pins SPEC-FRIEND’s cleanup guard: a report and
// origin proof permit removal, while active or unpublished work stays recoverable.
func TestTheDaemonKeepsJobsUnderTheCapAndReportsSpace(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                      string
		report, push, dirty, live bool
		removed                   bool
		dry                       bool
	}{
		{name: "published LAND", report: true, push: true, removed: true},
		{name: "unreported", push: true},
		{name: "unpushed", report: true},
		{name: "uncommitted", report: true, push: true, dirty: true},
		{name: "active", report: true, push: true, live: true},
		{name: "dry run", report: true, push: true, dry: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			g := testkit.Git(t, 1)
			g.Commit(g.Clones[0], map[string]string{"README.md": "base\n"})
			env := stageEnv(t)
			gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
			dir := t.TempDir()
			stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
			p, ok := PacketOf(stagedCard("a.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
			require.True(t, ok)
			_, err := stager.Stage(context.Background(), p)
			require.NoError(t, err)
			capacity, err := measureJobCapacity(dir, func(string) (int64, *int64, error) { inodes := int64(50); return 1000, &inodes, nil })
			require.NoError(t, err)
			assert.Positive(t, capacity.Jobs)
			assert.Equal(t, int64(1000), capacity.Free)
			assert.Equal(t, int64(50), *capacity.Inodes)
			stager.JobsCap = capacity.Jobs + 2
			assert.ErrorContains(t, stager.Admit(3), "jobs cap")
			stager.JobsCap = 0
			checkout := filepath.Join(JobDir(dir, p.Job), "repo")
			g.Commit(checkout, map[string]string{"work.md": "finished\n"})
			head := gitIn(t, env, checkout, "rev-parse", "HEAD")
			if row.push {
				gitIn(t, env, checkout, "push", "-q", "origin", p.Branch)
			}
			if row.report {
				out := filepath.Join(dir, "outbox", p.Job)
				require.NoError(t, os.MkdirAll(out, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(out, "REPORT.md"), []byte(fmt.Sprintf("Verdict: LAND\nHead: %s\n", head)), 0o644))
			}
			if row.dirty {
				require.NoError(t, os.WriteFile(filepath.Join(checkout, "work.md"), []byte("not committed\n"), 0o644))
			}
			result, err := stager.GC(context.Background(), map[string]bool{p.Job: row.live}, row.dry, 0)
			pruned := result.Removed
			require.NoError(t, err)
			if row.removed {
				assert.Equal(t, []string{p.Job}, pruned)
				assert.NoDirExists(t, JobDir(dir, p.Job))
				assert.FileExists(t, filepath.Join(dir, "outbox", p.Job, "REPORT.md"))
			} else if row.dry {
				assert.Equal(t, int64(0), result.Freed)
				assert.Positive(t, result.Planned)
				assert.Zero(t, result.Classes["removed"])
				assert.DirExists(t, checkout)
			} else {
				assert.Empty(t, pruned, "absent lane alone never authorizes deleting unpublished work")
				assert.DirExists(t, checkout)
			}
		})
	}
}

func TestJobCapacityDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	external := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(external, "payload"), make([]byte, 1<<20), 0o644))
	jobs := filepath.Join(dir, JobsDir)
	require.NoError(t, os.Mkdir(jobs, 0o755))
	require.NoError(t, os.Symlink(external, filepath.Join(jobs, "foreign~1")))
	bytes, err := JobsBytes(dir)
	require.NoError(t, err)
	assert.Less(t, bytes, int64(1<<20))
	result, err := (&Stager{Dir: dir}).GC(context.Background(), nil, false, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Classes["unowned"])
	assert.FileExists(t, filepath.Join(external, "payload"))
}

func TestBlockedJobCollectorDoesNotHoldBackBeats(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.passive = true
	started := make(chan struct{})
	r.d.Prune = func(ctx context.Context, _ map[string]bool) ([]string, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r.at[1] = func() { <-started }
	r.run(t, 5)
	assert.Equal(t, 5, r.beats)
}

// Failed proofs cannot keep a later published job outside every future pass.
func TestUnpublishedJobsCannotStarveTheCollector(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "base\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	dir := t.TempDir()
	s := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	var last Packet
	for n := 0; n < 8; n++ {
		p, ok := PacketOf(stagedCard(fmt.Sprintf("c%d.w1", n), "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
		require.True(t, ok)
		_, err := s.Stage(context.Background(), p)
		require.NoError(t, err)
		last = p
	}
	checkout := filepath.Join(JobDir(dir, last.Job), "repo")
	head := gitIn(t, env, checkout, "rev-parse", "HEAD")
	gitIn(t, env, checkout, "push", "-q", "origin", last.Branch)
	out := filepath.Join(dir, "outbox", last.Job)
	require.NoError(t, os.MkdirAll(out, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(out, "REPORT.md"), []byte("Verdict: LAND\nHead: "+head+"\n"), 0o644))
	result, err := s.GC(context.Background(), nil, false, 0)
	require.NoError(t, err)
	assert.Empty(t, result.Removed)
	result, err = s.GC(context.Background(), nil, false, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{last.Job}, result.Removed)
}

func TestAJobClaimCannotCrossCollectionOrAReport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &Stager{Dir: dir}
	job := "a.w1~15"
	s.admission.Lock()
	holder, err := s.ClaimJob(job, "new lane", time.Time{})
	require.NoError(t, err)
	assert.NotEmpty(t, holder)
	s.admission.Unlock()
	out := filepath.Join(dir, "outbox", job)
	require.NoError(t, os.MkdirAll(out, 0o755))
	report := filepath.Join(out, "REPORT.md")
	require.NoError(t, os.WriteFile(report, []byte("reported"), 0o644))
	holder, err = s.ClaimJob(job, "new lane", time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "reported job", holder)
	assert.NoFileExists(t, laneMarkPath(dir, job))
	require.NoError(t, os.Remove(report))
	holder, err = s.ClaimJob(job, "new lane", time.Time{})
	require.NoError(t, err)
	assert.Empty(t, holder)
	mark, found := ReadLaneMark(dir, job)
	assert.True(t, found)
	assert.True(t, s.localJobProtected(job, mark, found))
}

func TestAdmissionRechecksScratchGrowthInsteadOfCachedMetrics(t *testing.T) {
	t.Parallel()
	jobs := int64(10)
	now := time.Time{}
	a := &CapacityAdmission{Cap: 20, Now: func() time.Time { return now }, Measure: func(string) (int64, error) { return jobs, nil }}
	assert.ErrorContains(t, a.Check(context.Background(), Card{}), "fresh admission measurement")
	a.Wait()
	assert.NoError(t, a.Check(context.Background(), Card{}))
	// The last admitted sample was below the cap, but the running job grew.
	jobs = 30
	assert.ErrorContains(t, a.Check(context.Background(), Card{}), "fresh admission measurement")
	a.Wait()
	assert.ErrorContains(t, a.Check(context.Background(), Card{}), "jobs cap 20 bytes refuses a new lane: jobs=30")
	jobs = 10
	assert.Error(t, a.Check(context.Background(), Card{}))
	a.Wait()
	now = now.Add(3 * BeatEvery)
	assert.ErrorContains(t, a.Check(context.Background(), Card{}), "expired")
}

// A slow walk is already stale when it completes (SPEC-FRIEND, jobs capacity).
func TestCapacityAdmissionCountsMeasurementTime(t *testing.T) {
	t.Parallel()
	for _, duration := range []time.Duration{BeatEvery, 2 * BeatEvery, 3 * BeatEvery} {
		t.Run(duration.String(), func(t *testing.T) {
			t.Parallel()
			now := time.Time{}
			a := &CapacityAdmission{Cap: 20, Now: func() time.Time { return now }, Measure: func(string) (int64, error) {
				now = now.Add(duration)
				return 10, nil
			}}
			assert.ErrorContains(t, a.Check(context.Background(), Card{}), "fresh admission measurement")
			a.Wait()
			err := a.Check(context.Background(), Card{})
			if duration > 2*BeatEvery {
				assert.ErrorContains(t, err, "expired")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

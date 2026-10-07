package friend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The jobs live outside the daemon's own lanes: a fresh running lane mark counts, an ended
// or stale one does not; a runner's pid file counts while its pid answers and the job has
// no REPORT.md, beside the working directory or above it.
func TestLiveJobsOnReadsFreshLaneMarksAndLiveRunnerPids(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	dir := filepath.Join(top, "working")
	now := t0.Add(time.Hour)
	mark := func(job, text string) {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(text), 0o644))
	}
	mark("fresh", LaneMarkRunning("bob/lane-1", now.Add(-LaneMarkEvery)))
	mark("stale", LaneMarkRunning("bob/lane-2", now.Add(-LaneMarkStale)))
	mark("ended", LaneMarkEnded("bob/lane-3"))
	mark("done", LaneMarkRunning("bob/lane-4", now))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", "done"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", "done", "REPORT.md"), []byte("# done\n"), 0o644))
	pid := func(runner, job, text string) {
		require.NoError(t, os.MkdirAll(runner, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(runner, job+".pid"), []byte(text), 0o644))
	}
	pid(filepath.Join(dir, RunnerDir), "inside", "100\n")
	pid(filepath.Join(top, RunnerDir), "above", "200\n")
	pid(filepath.Join(top, RunnerDir), "dead", "300\n")
	pid(filepath.Join(top, RunnerDir), "junk", "not a pid\n")
	pid(filepath.Join(top, RunnerDir), "reported", "400\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", "reported"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", "reported", "REPORT.md"), []byte("# reported\n"), 0o644))
	alive := func(pid int) bool { return pid == 100 || pid == 200 || pid == 400 }

	assert.Equal(t, []string{"above", "fresh", "inside"}, LiveJobsOn(dir, now, alive))
	assert.Equal(t, []string{"fresh"}, LiveJobsOn(dir, now, nil), "no liveness: no pid file is read")
	assert.Empty(t, LiveJobsOn("", now, alive))
	assert.Empty(t, LiveJobsOn(filepath.Join(top, "nowhere"), now, alive))
}

package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A daemon starting up over a lane's run that is still alive adopts it: the lane holds the
// card and waits on the process, no FAIL report is written over the working run, and the
// card ends as any run's does when the process exits with its report written (the stopgap
// runners' `adopt`; until now the daemon finished every started card failed at once,
// internal/friend/tla/LaneEnd.tla AdoptedNotFailed). A run whose process is gone is ended
// as before.
func TestARestartedDaemonAdoptsALiveLaneRunAndFinishesItsCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}, block: make(chan struct{})}
		r, state := laneRig(t, h, 2)
		c1 := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		oldOwner := "bob lane 1 (daemon old)"
		*state = LaneState{Sessions: map[int]string{1: "ses_1", 2: "ses_2"}, Started: map[string]Started{"c1~15": {Lane: 1, Card: c1, At: t0.Add(-time.Minute), RunID: "old-run", Owner: oldOwner}}}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c1~15"), 0o755))
		require.NoError(t, writeRunReceipt(dir, "c1~15", runReceipt{RunID: "old-run", PID: 4242, Identity: "birth-1"}))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, "c1~15"), []byte(LaneMarkRunningRun(oldOwner, t0, "old-run")), 0o644))
		var mu sync.Mutex
		alive := true
		r.d.ProcessAlive = func(pid int) bool { mu.Lock(); defer mu.Unlock(); return pid == 4242 && alive }
		r.d.ProcessIdentity = func(pid int) string {
			mu.Lock()
			defer mu.Unlock()
			if pid == 4242 && alive {
				return "birth-1"
			}
			return ""
		}
		ended := make(chan struct{})
		r.d.WaitProcess = func(ctx context.Context, pid int) bool {
			require.Equal(t, 4242, pid)
			select {
			case <-ended:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var holding Status
		var transferred LaneMark
		r.at[4] = func() { holding = r.last(); transferred, _ = ReadLaneMark(dir, "c1~15") }
		r.at[6] = func() { // the live run finishes its card and exits
			require.NoError(t, os.MkdirAll(c1.Outbox, 0o755))
			require.NoError(t, os.WriteFile(c1.Report(), []byte("Verdict: LAND\nHead: abc\n\nfine\n"), 0o644))
			require.NoError(t, os.WriteFile(c1.Result(), []byte("RESULT: c1\n"), 0o644))
			mu.Lock()
			alive = false
			mu.Unlock()
			close(ended)
		}
		r.at[8] = func() { close(h.block) } // lane 2's own card ends after
		r.run(t, 14)
		turns, _, seeds := h.got()
		assert.Empty(t, seeds, "both lanes keep their sessions")
		assert.Equal(t, []string{"ses_2: c2"}, turns, "lane 1 waits on the adopted run and pushes nothing; lane 2 works on")
		assert.Equal(t, "1:ses_1:c1/1 2:ses_2:c2/1", holding.Lanes, "the adopted card is in lane 1's hand")
		assert.NotEqual(t, oldOwner, transferred.Who, "the fresh old mark transferred before other-lane exclusion")
		assert.False(t, transferred.Ended)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "card c1 was begun at "+t0.Add(-time.Minute).UTC().Format(time.RFC3339)+" by the daemon before, and its run (pid 4242) is alive: adopted")
		assert.Contains(t, records, "lane 1: card c1 adopted (pid 4242): the lane waits on the run")
		assert.NotContains(t, records, "its run is gone", "no FAIL over a working run")
		assert.Contains(t, records, `subject="card c1 (adopted)" messages=0`)
		assert.Contains(t, records, "card=done finish=report")
		raw, err := os.ReadFile(c1.Report())
		require.NoError(t, err)
		assert.Equal(t, "Verdict: LAND\nHead: abc\n\nfine\n", string(raw), "the friend's own report stands")
		assert.Empty(t, state.GivenUp, "a card finished by its adopted run is not set aside")
		assert.Empty(t, state.Started)
	})
}

func TestALaneDoesNotLaunchWithoutDurableStartedState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		r.d.SaveLanes = func(LaneState) error { return errors.New("disk unavailable") }
		r.run(t, 5)
		turns, _, _ := h.got()
		assert.Empty(t, turns)
		assert.Empty(t, state.Started)
		assert.NoFileExists(t, laneMarkPath(dir, "c1~15"))
		assert.Contains(t, strings.Join(r.records, "\n"), "not started: lane state cannot be saved: disk unavailable")
	})
}

// A started card whose process is gone is ended as before: the lane's FAIL report, the
// card set aside; and a run the exec started has its process id on the started mark, so the
// next daemon can tell the two apart.
func TestAStartedCardWhoseRunIsGoneIsEndedAndARunsPidIsRecorded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}, block: make(chan struct{})}
		h.pid = 777
		r, state := laneRig(t, h, 1)
		c1 := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{"c1~15": {Lane: 1, Card: c1, At: t0.Add(-time.Minute), Pid: 4242}}}
		r.d.ProcessAlive = func(pid int) bool { return false }
		r.d.ProcessIdentity = func(pid int) string { return "birth-2" }
		midPid, midStarted := 0, false
		r.at[6] = func() {
			st, ok := state.Started["c2~15"]
			midPid, midStarted = st.Pid, ok
		}
		r.at[8] = func() { close(h.block) }
		r.run(t, 12)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "card c1 was begun at "+t0.Add(-time.Minute).UTC().Format(time.RFC3339)+" and its run is gone: finish=failed")
		assert.Equal(t, []string{"c1~15"}, state.GivenUp)
		require.True(t, midStarted, "the running card is marked started")
		assert.Equal(t, 777, midPid, "with the process the exec handed")
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: c2"}, turns)
	})
}

func TestAReusedPIDCannotAdoptAnotherRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{"c1~15": {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "run-1", Owner: "old"}}}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c1~15"), 0o755))
		require.NoError(t, writeRunReceipt(dir, "c1~15", runReceipt{RunID: "run-1", PID: 9999999, Identity: "old-birth"}))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, "c1~15"), []byte(LaneMarkRunningRun("old", t0, "run-1")), 0o644))
		r.d.ProcessIdentity = func(pid int) string { require.Equal(t, 9999999, pid); return "new-birth" }
		r.run(t, 3)
		assert.Contains(t, strings.Join(r.records, "\n"), "its run is gone: finish=failed")
		assert.FileExists(t, card.Report())
		assert.Empty(t, state.Started)
	})
}

func TestASecondRestartTransfersTheSameRunAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		job, oldOwner := "c1~15", "bob lane 1 (daemon first)"
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
		require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "same-run", PID: 9999999, Identity: "birth"}))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun(oldOwner, t0, "same-run")), 0o644))
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		state := LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{job: {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "same-run", Owner: oldOwner}}}
		var owners []string
		for i := 0; i < 2; i++ {
			r, saved := laneRig(t, h, 1)
			*saved = state
			r.d.ProcessIdentity = func(int) string { return "birth" }
			r.d.WaitProcess = func(ctx context.Context, _ int) bool { <-ctx.Done(); return false }
			r.run(t, 3)
			state = *saved
			mark, ok := ReadLaneMark(dir, job)
			require.True(t, ok)
			assert.Equal(t, "same-run", mark.RunID)
			assert.False(t, mark.Ended)
			owners = append(owners, mark.Who)
			assert.NoFileExists(t, card.Report())
		}
		assert.NotEqual(t, oldOwner, owners[0])
		assert.NotEqual(t, owners[0], owners[1])
		assert.Contains(t, state.Started, job)
	})
}

func TestRecoveryCannotTakeAMissingLaneMark(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	holder, err := transferLane(dir, "c1~15", "same-run", "new", t0)
	require.ErrorContains(t, err, "lane mark missing")
	assert.Empty(t, holder)
	assert.NoFileExists(t, laneMarkPath(dir, "c1~15"))
}

func TestOlderStartedStateCannotFailAVerifiedForeignRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		job, owner := "c1~15", "bob lane 1 (daemon new)"
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
		require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "new-run", PID: 9999999, Identity: "new-birth"}))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun(owner, t0, "new-run")), 0o644))
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{job: {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "old-run", Owner: "old"}}}
		r.d.ProcessIdentity = func(int) string { return "new-birth" }
		r.run(t, 3)
		assert.NoFileExists(t, card.Report())
		assert.Equal(t, "new-run", state.Started[job].RunID)
		mark, found := ReadLaneMark(dir, job)
		require.True(t, found)
		assert.Equal(t, owner, mark.Who)
		assert.False(t, mark.Ended)
	})
}

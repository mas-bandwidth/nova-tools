//go:build unix

package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCannotExecuteBeforeItsDurableIdentityReceipt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	marker := filepath.Join(dir, "executed")
	entered, release := make(chan struct{}), make(chan struct{})
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		gate := WithProcessStarted(ctx, func(pid int) error {
			close(entered)
			<-release
			return writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: pid, Identity: ProcessIdentity(pid)})
		})
		_, _, err := RealExec(gate, dir, "/bin/sh", []string{"-c", "printf yes > executed"}, "")
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("launch callback did not start: %v", err)
	}
	assert.NoFileExists(t, marker, "the child is still behind its launch gate")
	close(release)
	require.NoError(t, <-done)
	assert.FileExists(t, marker)
	receipt, err := readRunReceipt(dir, job)
	require.NoError(t, err)
	assert.Equal(t, "run-1", receipt.RunID)
	assert.Positive(t, receipt.PID)
	assert.NotEmpty(t, receipt.Identity)
}

func TestRestartTransfersAFreshMarkForARealSurvivingProcess(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		cmd := exec.CommandContext(context.Background(), "/bin/sleep", "10")
		ownGroup(cmd)
		require.NoError(t, cmd.Start())
		defer func() { killGroup(cmd.Process.Pid); _ = cmd.Wait() }()
		identity := ProcessIdentity(cmd.Process.Pid)
		require.NotEmpty(t, identity)
		job, owner := "c1~15", "bob lane 1 (daemon 99999998.1)"
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
		require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "real-run", PID: cmd.Process.Pid, Identity: identity}))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun(owner, t0, "real-run")), 0o644))
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{job: {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "real-run", Owner: owner}}}
		r.d.ProcessIdentity = ProcessIdentity
		r.d.ProcessAlive = ProcessAlive
		r.d.WaitProcess = func(ctx context.Context, _ int) bool { <-ctx.Done(); return false }
		var mark LaneMark
		r.at[3] = func() { mark, _ = ReadLaneMark(dir, job) }
		r.run(t, 4)
		assert.NotEqual(t, owner, mark.Who)
		assert.False(t, mark.Ended)
		assert.NoFileExists(t, card.Report(), "restart did not fail a working process")
		assert.Equal(t, identity, ProcessIdentity(cmd.Process.Pid))
	})
}

func TestStaleMarkCannotBeClaimedOverItsLiveRealRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "c1~15"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	cmd := exec.CommandContext(context.Background(), "/bin/sleep", "10")
	ownGroup(cmd)
	require.NoError(t, cmd.Start())
	defer func() { killGroup(cmd.Process.Pid); _ = cmd.Wait() }()
	identity := ProcessIdentity(cmd.Process.Pid)
	require.NotEmpty(t, identity)
	require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "live-run", PID: cmd.Process.Pid, Identity: identity}))
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun("old", now.Add(-LaneMarkStale-time.Second), "live-run")), 0o644))
	holder, err := ClaimLane(dir, job, "new", now)
	require.NoError(t, err)
	assert.Equal(t, "old", holder)
	mark, ok := ReadLaneMark(dir, job)
	require.True(t, ok)
	assert.Equal(t, "live-run", mark.RunID)
	assert.Equal(t, "old", mark.Who)
	require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "live-run", PID: cmd.Process.Pid, Identity: identity + "-reused"}))
	holder, err = ClaimLane(dir, job, "new", now)
	require.NoError(t, err)
	assert.Empty(t, holder, "the live process has a different birth and cannot hold an old run")
}

func TestOlderStartedCannotFailAStaleMarkedLiveForeignRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		job, owner := "c1~15", "new-owner"
		cmd := exec.CommandContext(context.Background(), "/bin/sleep", "10")
		ownGroup(cmd)
		require.NoError(t, cmd.Start())
		defer func() { killGroup(cmd.Process.Pid); _ = cmd.Wait() }()
		identity := ProcessIdentity(cmd.Process.Pid)
		require.NotEmpty(t, identity)
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
		require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "new-run", PID: cmd.Process.Pid, Identity: identity}))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun(owner, t0.Add(-LaneMarkStale-time.Second), "new-run")), 0o644))
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{job: {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "old-run", Owner: "old-owner"}}}
		r.d.ProcessIdentity = ProcessIdentity
		r.run(t, 3)
		assert.NoFileExists(t, card.Report())
		assert.Equal(t, "new-run", state.Started[job].RunID)
		assert.Equal(t, identity, ProcessIdentity(cmd.Process.Pid))
	})
}

func TestRecoveryCannotTransferFromAStillRunningDaemon(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "c1~15"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	owner := fmt.Sprintf("bob lane 1 (daemon %d.1)", os.Getpid())
	require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun(owner, t0, "live-run")), 0o644))
	holder, err := transferLane(dir, job, "live-run", "new-owner", t0, ProcessIdentity, ProcessAlive)
	require.NoError(t, err)
	assert.Equal(t, owner, holder)
	mark, found := ReadLaneMark(dir, job)
	require.True(t, found)
	assert.Equal(t, owner, mark.Who)
}

func TestStopVerifiedRunRefusesAReusedPID(t *testing.T) {
	t.Parallel()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", "sleep 10")
	ownGroup(cmd)
	require.NoError(t, cmd.Start())
	defer func() { killGroup(cmd.Process.Pid); _ = cmd.Wait() }()
	identity := ProcessIdentity(cmd.Process.Pid)
	require.NotEmpty(t, identity)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.False(t, StopVerifiedRun(ctx, cmd.Process.Pid, identity+"-old"))
	assert.False(t, runStillAlive(cmd.Process.Pid, identity+"-old"))
	assert.Equal(t, identity, ProcessIdentity(cmd.Process.Pid), "the unrelated live group was not signalled")
}

func TestAFailedReceiptNeverReleasesTheHarness(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	ctx := WithProcessStarted(context.Background(), func(int) error { return errors.New("receipt failed") })
	_, _, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "printf yes > executed"}, "")
	require.ErrorContains(t, err, "receipt failed")
	assert.NoFileExists(t, marker)
}

//go:build unix

package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A STOP debt can be returned only after the durable birth identifies the
// actual native group and every member has exited.
func TestStopDebtWaitsForItsRealProcessGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	cmd := exec.CommandContext(context.Background(), "/bin/sleep", "10")
	ownGroup(cmd)
	require.NoError(t, cmd.Start())
	defer func() { killGroup(cmd.Process.Pid); _ = cmd.Wait() }()
	identity := ProcessIdentity(cmd.Process.Pid)
	require.NotEmpty(t, identity)
	require.NoError(t, procgroup.StartAnchor(cmd.Process.Pid, runReceiptPath(dir, job)+".anchor"))
	card := Card{ID: "card"}
	st := Started{Lane: 1, Card: card, RunID: "run-1"}
	debt := StopReturn{Lane: 1, Job: job, Row: "friend.test", Card: card.ID, Gen: card.Gen(), Epoch: card.Epoch(), Exit: -1, At: time.Now()}
	s := LaneState{Started: map[string]Started{job: st}, StopReturns: []StopReturn{debt}}
	d := &Daemon{Dir: dir, ProcessIdentity: ProcessIdentity, SaveLanes: func(LaneState) error { return nil }}
	l := &loop{d: d, lanes: &laneSet{state: s}}
	require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: st.RunID, PID: cmd.Process.Pid, Identity: identity + "-reused"}))
	l.settleStopDebts(time.Now())
	assert.False(t, l.lanes.state.StopReturns[0].Ended, "a reused PID cannot prove the old group exited")
	assert.Contains(t, l.lanes.state.Started, job)
	assert.Equal(t, identity, ProcessIdentity(cmd.Process.Pid), "the foreign live group was not signalled")
	require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: st.RunID, PID: cmd.Process.Pid, Identity: identity}))
	l.settleStopDebts(time.Now())
	assert.True(t, l.lanes.state.StopReturns[0].Ended)
	assert.NotContains(t, l.lanes.state.Started, job)
	assert.False(t, ProcessGroupAlive(cmd.Process.Pid))
}

func TestAnchoredRealExecReapsItsGroupOnOrdinaryExit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	pid := 0
	ctx := WithProcessStarted(context.Background(), func(started int) error {
		pid = started
		return writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: pid, Identity: ProcessIdentity(pid)})
	})
	ctx = WithProcessGroupAnchor(ctx, runReceiptPath(dir, job)+".anchor")
	_, exit, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "exit 0"}, "")
	require.NoError(t, err)
	assert.Zero(t, exit)
	assert.Positive(t, pid)
	assert.False(t, procgroup.GroupRunnable(pid), "normal completion removes the anchor and descendants")
}

func TestAnchoredRealExecCancelBeforeGateDoesNotLaunchHarness(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	marker := filepath.Join(dir, "executed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	pid := 0
	ctx = WithProcessStarted(ctx, func(started int) error {
		pid = started
		close(entered)
		<-release
		return writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: started, Identity: ProcessIdentity(started)})
	})
	ctx = WithProcessGroupAnchor(ctx, runReceiptPath(dir, job)+".anchor")
	done := make(chan error, 1)
	go func() {
		_, _, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "echo yes > executed"}, "")
		done <- err
	}()
	require.Eventually(t, func() bool {
		select {
		case <-entered:
			return true
		default:
			return false
		}
	}, 30*time.Second, 10*time.Millisecond, "process-start callback did not begin")
	cancel()
	close(release)
	var runErr error
	require.Eventually(t, func() bool {
		select {
		case runErr = <-done:
			return true
		default:
			return false
		}
	}, 30*time.Second, 10*time.Millisecond, "cancelled gated process did not return")
	require.Error(t, runErr)
	assert.NoFileExists(t, marker)
	assert.Positive(t, pid)
	assert.False(t, procgroup.GroupRunnable(pid))
}

func TestAnchoredRealExecContextCancelReapsResistantGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pid := 0
	ctx = WithProcessStarted(ctx, func(started int) error {
		pid = started
		return writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: started, Identity: ProcessIdentity(started)})
	})
	ctx = WithProcessGroupAnchor(ctx, runReceiptPath(dir, job)+".anchor")
	done := make(chan error, 1)
	go func() {
		_, _, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "trap '' TERM; echo ready > \"$1\"; while :; do sleep 1; done", "sh", ready}, "")
		done <- err
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 30*time.Second, 10*time.Millisecond)
	cancel()
	var runErr error
	require.Eventually(t, func() bool {
		select {
		case runErr = <-done:
			return true
		default:
			return false
		}
	}, 30*time.Second, 10*time.Millisecond, "cancelled resistant group did not return")
	require.Error(t, runErr)
	assert.Positive(t, pid)
	assert.False(t, procgroup.GroupRunnable(pid), "context cancellation must reap the whole verified group")
}

func TestUnreceiptedRealExecCancelDoesNotClaimGroupExit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithOutputSeen(ctx, cancel)
	_, _, err := RealExec(ctx, t.TempDir(), "/bin/sh", []string{"-c", "echo ready; exec sleep 30"}, "")
	require.ErrorContains(t, err, "unreceipted descendants are unverified")
}

func TestStopDebtReapsLeaderlessGroupThroughPinnedAnchor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	leader := exec.CommandContext(context.Background(), "/bin/sleep", "10")
	ownGroup(leader)
	require.NoError(t, leader.Start())
	pgid := leader.Process.Pid
	defer killGroup(pgid)
	identity := ProcessIdentity(pgid)
	require.NotEmpty(t, identity)
	anchorPath := runReceiptPath(dir, job) + ".anchor"
	require.NoError(t, procgroup.StartAnchor(pgid, anchorPath))
	child := exec.CommandContext(context.Background(), "/bin/sleep", "10")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	require.NoError(t, child.Start())
	defer func() { killGroup(pgid); _ = child.Wait() }()
	require.NoError(t, leader.Process.Kill())
	require.Error(t, leader.Wait(), "the test killed its group leader")
	require.Empty(t, ProcessIdentity(pgid), "the group leader has exited")
	require.True(t, procgroup.GroupRunnable(pgid), "the descendant and anchor still run")
	require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: pgid, Identity: identity}))
	card := Card{ID: "card"}
	s := LaneState{Started: map[string]Started{job: {Lane: 1, Card: card, RunID: "run-1"}}, StopReturns: []StopReturn{{Job: job, Card: card.ID, Gen: card.Gen(), Epoch: card.Epoch(), Exit: -1, At: time.Now()}}}
	l := &loop{d: &Daemon{Dir: dir, ProcessIdentity: ProcessIdentity, SaveLanes: func(LaneState) error { return nil }}, lanes: &laneSet{state: s}}
	anchor, err := os.ReadFile(anchorPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(anchorPath))
	l.settleStopDebts(time.Now())
	assert.False(t, l.lanes.state.StopReturns[0].Ended, "a lost group pin cannot return live descendants")
	assert.Contains(t, l.lanes.state.Started, job)
	assert.True(t, procgroup.GroupRunnable(pgid))
	require.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
	l.settleStopDebts(time.Now())
	assert.True(t, l.lanes.state.StopReturns[0].Ended)
	assert.NotContains(t, l.lanes.state.Started, job)
	assert.False(t, procgroup.GroupRunnable(pgid), "STOP reaped the descendant and anchor")
}

// A retired lane releases capacity only from native group exit proof, never
// just because its harness call returned. This also pins failure atomicity.
func TestRetiredWorkReservationWaitsForNativeGroupExitAndDurableSave(t *testing.T) {
	t.Parallel()
	dir, job := t.TempDir(), "c1~15"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0755))
	cmd := exec.CommandContext(context.Background(), "/bin/sleep", "30")
	ownGroup(cmd)
	require.NoError(t, cmd.Start())
	waited := false
	defer func() {
		if !waited {
			killGroup(cmd.Process.Pid)
			_ = cmd.Wait()
		}
	}()
	identity := ProcessIdentity(cmd.Process.Pid)
	require.NotEmpty(t, identity)
	card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
	ln := &lane{n: 2, card: &card}
	st := Started{Lane: 2, Card: card, RunID: "retired-run", Pid: cmd.Process.Pid}
	failSave := false
	d := &Daemon{Dir: dir, Friend: "bob", Record: func(string) {}, ProcessIdentity: ProcessIdentity, SaveLanes: func(LaneState) error {
		if failSave {
			return fmt.Errorf("state write failed")
		}
		return nil
	}}
	l := &loop{d: d, tag: "fixture", lanes: &laneSet{lanes: []*lane{ln}, state: LaneState{Started: map[string]Started{job: st}}}, reads: &readSet{}}
	owner := l.laneWho(ln.n)
	require.NoError(t, os.MkdirAll(filepath.Dir(laneMarkPath(dir, job)), 0755))
	require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun(owner, t0, st.RunID)), 0644))
	receipt := runReceipt{RunID: st.RunID, PID: cmd.Process.Pid, Identity: identity}
	require.NoError(t, writeRunReceipt(dir, job, receipt))
	assert.False(t, l.releaseIdleReservation(ln, t0), "a live matching native group keeps its child slot")
	assert.Contains(t, l.lanes.state.Started, job)
	mark, found := ReadLaneMark(dir, job)
	require.True(t, found)
	assert.Equal(t, owner, mark.Who)
	assert.Equal(t, identity, ProcessIdentity(cmd.Process.Pid), "release does not kill existing shrink children")
	killGroup(cmd.Process.Pid)
	_ = cmd.Wait()
	waited = true
	require.False(t, procgroup.GroupRunnable(cmd.Process.Pid), "the group's exit is observed before capacity is released")
	for _, tc := range []struct {
		name      string
		receipt   runReceipt
		malformed bool
		saveFails bool
	}{
		{name: "different-run", receipt: runReceipt{RunID: "other-run", PID: receipt.PID, Identity: identity}},
		{name: "malformed", malformed: true},
		{name: "nonpositive-pid", receipt: runReceipt{RunID: st.RunID, PID: 0, Identity: identity}},
		{name: "state-save-failed", receipt: receipt, saveFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.receipt)
			if tc.malformed {
				raw = []byte("{")
			}
			require.NoError(t, os.WriteFile(runReceiptPath(dir, job), raw, 0644))
			failSave = tc.saveFails
			assert.False(t, l.releaseIdleReservation(ln, t0), "unproved or uncommitted release must keep reservation")
			assert.Contains(t, l.lanes.state.Started, job)
			mark, found := ReadLaneMark(dir, job)
			require.True(t, found)
			assert.Equal(t, owner, mark.Who, "failed save retains the owning claim")
		})
	}
	failSave = false
	require.NoError(t, writeRunReceipt(dir, job, receipt))
	require.True(t, l.releaseIdleReservation(ln, t0), "matching observed exit plus durable removal releases capacity")
	assert.NotContains(t, l.lanes.state.Started, job)
	_, found = ReadLaneMark(dir, job)
	assert.False(t, found)
}

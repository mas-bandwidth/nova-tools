//go:build linux

package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/require"
)

func TestFailedLaneStateReadCannotOverwriteDurableStarted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		stateDir := t.TempDir()
		card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", "c1~15")}
		owed := LaneState{Started: map[string]Started{"c1~15": {Lane: 1, Card: card, At: t0.Add(-time.Minute)}}}
		require.NoError(t, WriteLanes(stateDir, owed))
		r.d.StateDir = stateDir
		// A decoder can return useful-looking fields alongside a later field
		// error. None of that partial state is safe to replay or save.
		r.d.LoadLanes = func() (LaneState, error) { return owed, errors.New("lane state read refused") }
		writes := 0
		r.d.SaveLanes = func(s LaneState) error { writes++; return WriteLanes(stateDir, s) }
		r.run(t, 4) // a control session may open while card admission is held
		saved, err := ReadLanes(stateDir)
		require.NoError(t, err)
		require.Contains(t, saved.Started, "c1~15")
		require.Zero(t, writes, "an incomplete in-memory placeholder must never replace durable claims")
		require.NoFileExists(t, card.Report(), "partial Started cannot be recovered as a completed run")
	})
}

func TestMissingRunAfterRecordedLaunchRetainsLaneClaim(t *testing.T) {
	t.Parallel()
	dir, job := t.TempDir(), "c1~15"
	card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
	st := Started{Lane: 1, Card: card, RunID: "run-1", Pid: 9999999}
	state := LaneState{Started: map[string]Started{job: st}}
	writes := 0
	d := &Daemon{Dir: dir, ProcessIdentity: ProcessIdentity, SaveLanes: func(LaneState) error { writes++; return nil }}
	ln := &lane{n: 1, card: &card}
	l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{lanes: []*lane{ln}, state: state}}
	require.False(t, l.stopRunQuiescent(job, st), "a lost RUN is not no-launch proof after a PID was recorded")
	require.False(t, l.releaseIdleReservation(ln, time.Now()))
	require.Contains(t, l.lanes.state.Started, job)
	require.Zero(t, writes)
	st.Pid = 0
	require.True(t, l.stopRunQuiescent(job, st), "a missing RUN before launch remains an idle reservation")
}

func TestBatchRestartSettlesPriorOneShotCompletionWithoutLaunchingLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		r.d.Row = func() (string, int) { return ModeBatch, 1 }
		r.d.StateDir = t.TempDir()
		r.d.Rules = func() LaneRules { return LaneRules{} }
		job := "c1~15"
		card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
		require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
		require.NoError(t, os.WriteFile(card.Result(), []byte("RESULT: prior one-shot turn complete\n"), 0o644))
		state.Started = map[string]Started{job: {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "old-run"}}
		r.run(t, 4)
		require.Empty(t, state.Started, "batch mode still settles the durable prior completion")
		require.FileExists(t, card.Report())
		require.Contains(t, fmt.Sprint(r.adaGot(t)), "bob card c1~15", "the completion note is durably sent by the native retry path")
		turns, _, _ := h.got()
		require.Empty(t, turns, "batch recovery must not start a one-shot turn")
	})
}

func TestBatchRestartHoldsNewWorkWhilePriorOneShotGroupUnproved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		r.d.Row = func() (string, int) { return ModeBatch, 1 }
		job := "c1~15"
		card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
		state.Started = map[string]Started{job: {Lane: 1, Card: card, At: t0.Add(-time.Minute), RunID: "old-run", Pid: 9999999}}
		r.send(t, "ada", "new work", "do new batch work")
		r.run(t, 4)
		require.Contains(t, state.Started, job)
		require.Empty(t, r.delivered, "unproved old capacity blocks new batch provider work")
		turns, _, _ := h.got()
		require.Empty(t, turns)
	})
}

func TestAdoptedResultDoesNotInventOriginalExitOrWall(t *testing.T) {
	t.Parallel()
	dir, stateDir, job := t.TempDir(), t.TempDir(), "c1~15"
	card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	require.NoError(t, os.WriteFile(card.Result(), []byte("RESULT: original run complete\n"), 0o644))
	st := Started{Lane: 1, Card: card, At: time.Now().Add(-time.Minute), RunID: "old-run"}
	state := LaneState{Started: map[string]Started{job: st}}
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(string) {}, Rules: func() LaneRules { return LaneRules{} }, ProcessIdentity: ProcessIdentity, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }}
	d.m = Start(time.Now())
	turn := &turn{started: time.Now().Add(-time.Second), adopted: true}
	ln := &lane{n: 1, card: &card, t: turn}
	l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{lanes: []*lane{ln}, state: state, given: map[string]bool{}}}
	l.laneDone(laneResult{ln: ln, t: turn, turn: LaneTurn{Exit: 0}}, time.Now())
	require.Nil(t, ln.card)
	report, err := os.ReadFile(card.Report())
	require.NoError(t, err)
	require.Contains(t, string(report), "the run's exit and wall are unavailable")
	require.NotContains(t, string(report), "exit 0")
	note, err := os.ReadFile(finishNoticePath(d, job))
	require.NoError(t, err)
	require.Contains(t, string(note), "wall unknown")
}

func TestPendingLanePassBoundsSlowFinishAndRotatesDebt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		dir, stateDir := t.TempDir(), t.TempDir()
		state := LaneState{Started: map[string]Started{}}
		lanes := make([]*lane, 16)
		for i := range lanes {
			job := fmt.Sprintf("c%02d~15", i)
			card := Card{ID: fmt.Sprintf("c%02d", i), Outbox: filepath.Join(dir, "outbox", job)}
			end := LaneEnd{Exit: 7, Started: now.Add(-time.Minute), Wall: time.Minute}
			state.Started[job] = Started{Lane: i + 1, Card: card, At: end.Started, Ending: &end}
			lanes[i] = &lane{n: i + 1, card: &card, pendingEnd: &end}
		}
		require.NoError(t, WriteLanes(stateDir, state))
		calls := 0
		d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Record: func(string) {}, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }, Finish: func(ctx context.Context, _ []string) error {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}}
		l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{lanes: lanes, state: state, given: map[string]bool{}}}
		finished := func() int {
			n := 0
			for _, ln := range lanes {
				if ln.card == nil {
					n++
				}
			}
			return n
		}
		before := time.Now()
		l.retryPendingLanes(now)
		require.LessOrEqual(t, time.Since(before), PendingLanePassBudget)
		require.Equal(t, 1, finished())
		before = time.Now()
		l.retryPendingLanes(now.Add(time.Second))
		require.LessOrEqual(t, time.Since(before), PendingLanePassBudget)
		require.Equal(t, 1+PendingLanePassLimit, finished(), "a slow first debt does not starve later lanes")
		before = time.Now()
		l.retryPendingLanes(now.Add(2 * time.Second))
		require.LessOrEqual(t, time.Since(before), PendingLanePassBudget)
		require.Equal(t, len(lanes), finished())
		require.Empty(t, l.lanes.state.Started)
	})
}

// A returned shell leader can leave a live descendant. No RESULT, REPORT,
// continuation or capacity release is allowed until its matching RUN is gone.
func TestLaneCompletionWaitsForMatchingProcessGroup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		result  bool
		stopped bool
	}{{name: "continuation"}, {name: "result", result: true}, {name: "result-under-stop", result: true, stopped: true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, stateDir, job := t.TempDir(), t.TempDir(), "c1~15"
			card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
			require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
			if tc.result {
				require.NoError(t, os.WriteFile(card.Result(), []byte("RESULT: work complete\n"), 0o644))
			}
			anchorPath := runReceiptPath(dir, job) + ".anchor"
			var anchor []byte
			var anchorErr error
			var lost sync.Once
			pid := 0
			ctx := WithProcessStarted(context.Background(), func(started int) error {
				pid = started
				return writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: pid, Identity: ProcessIdentity(pid)})
			})
			ctx = WithProcessGroupAnchor(ctx, anchorPath)
			ctx = WithOutputSeen(ctx, func() {
				lost.Do(func() {
					anchor, anchorErr = os.ReadFile(anchorPath)
					if anchorErr == nil {
						anchorErr = os.Remove(anchorPath)
					}
				})
			})
			defer func() {
				if len(anchor) > 0 && procgroup.GroupRunnable(pid) {
					require.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					require.True(t, procgroup.ReapVerified(cleanupCtx, pid, "", anchorPath, 100*time.Millisecond))
				}
			}()
			_, exit, err := realExec(ctx, 100*time.Millisecond, dir, "/bin/sh", []string{"-c", "sleep 30 >/dev/null 2>&1 & echo ready; exit 7"}, "")
			require.NoError(t, anchorErr)
			require.Equal(t, 7, exit)
			require.True(t, procgroup.GroupRunnable(pid))
			started := Started{Lane: 1, Card: card, At: time.Now().Add(-time.Minute), RunID: "run-1", Pid: pid}
			state := LaneState{Started: map[string]Started{job: started}}
			require.NoError(t, WriteLanes(stateDir, state))
			d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(string) {}, Rules: func() LaneRules { return LaneRules{} }, MachineStopped: func() bool { return tc.stopped }, ProcessIdentity: ProcessIdentity, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }}
			d.m = Start(time.Now())
			ln := &lane{n: 1, card: &card, t: &turn{started: time.Now().Add(-time.Second)}, session: "session"}
			l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{lanes: []*lane{ln}, state: state, given: map[string]bool{}}}
			now := time.Now()
			l.laneDone(laneResult{ln: ln, t: ln.t, turn: LaneTurn{Exit: exit}, err: err}, now)
			require.NotNil(t, ln.card, "the lane retains its capacity")
			require.NotNil(t, ln.pendingResult, "the returned result is held")
			require.Contains(t, l.lanes.state.Started, job)
			require.NoFileExists(t, card.Report())
			if tc.result {
				require.NotNil(t, l.lanes.state.Started[job].Ending, "the native exit is frozen before a possible daemon restart")
				require.NoFileExists(t, finishNoticePath(d, job))
			}
			if tc.stopped {
				require.Empty(t, l.lanes.state.StopReturns, "completed work is held for group proof rather than returned")
			}
			require.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			require.True(t, procgroup.ReapVerified(cleanupCtx, pid, "", anchorPath, 100*time.Millisecond))
			cancel()
			l.retryPendingLanes(now.Add(time.Second))
			require.Nil(t, ln.pendingResult)
			if tc.result {
				require.Nil(t, ln.card)
				require.FileExists(t, card.Report())
				raw, readErr := os.ReadFile(card.Report())
				require.NoError(t, readErr)
				require.Regexp(t, `exit(?:ed)? 7`, string(raw))
				require.NotContains(t, string(raw), "run is gone")
				require.FileExists(t, finishNoticePath(d, job))
			} else {
				require.NotNil(t, ln.card, "the same card may continue only after group exit")
				require.NoFileExists(t, card.Report())
			}
		})
	}
}

func TestFirstCompletedLaneResultAfterStopSettlesItsReport(t *testing.T) {
	t.Parallel()
	dir, stateDir, job := t.TempDir(), t.TempDir(), "c2~15"
	card := Card{ID: "c2", Outbox: filepath.Join(dir, "outbox", job)}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	require.NoError(t, os.WriteFile(card.Result(), []byte("RESULT: completed before STOP\n"), 0o644))
	require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: "run-2", PID: 9999999, Identity: "old-birth"}))
	now := time.Now()
	started := Started{Lane: 1, Card: card, At: now.Add(-time.Minute), RunID: "run-2", Pid: 9999999}
	state := LaneState{Started: map[string]Started{job: started}}
	require.NoError(t, WriteLanes(stateDir, state))
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(string) {}, Rules: func() LaneRules { return LaneRules{} }, MachineStopped: func() bool { return true }, ProcessIdentity: func(int) string { return "" }, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }}
	d.m = Start(now)
	ln := &lane{n: 1, card: &card, session: "session", t: &turn{started: now.Add(-time.Second)}}
	l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{lanes: []*lane{ln}, state: state, given: map[string]bool{}}}
	l.laneDone(laneResult{ln: ln, t: ln.t, turn: LaneTurn{Exit: 0}}, now)
	require.Nil(t, ln.card)
	require.Empty(t, l.lanes.state.StopReturns, "a completed quiescent result is not returned as cancelled work")
	require.Empty(t, l.lanes.state.Started)
	require.FileExists(t, card.Report())
	require.FileExists(t, finishNoticePath(d, job))
}

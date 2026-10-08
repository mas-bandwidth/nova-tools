//go:build linux

package friend

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/require"
)

// A returned shell leader can leave a live descendant. No RESULT, REPORT,
// continuation or capacity release is allowed until its matching RUN is gone.
func TestLaneCompletionWaitsForMatchingProcessGroup(t *testing.T) {
	t.Parallel()
	for _, result := range []bool{false, true} {
		t.Run(map[bool]string{false: "continuation", true: "result"}[result], func(t *testing.T) {
			dir, stateDir, job := t.TempDir(), t.TempDir(), "c1~15"
			card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", job)}
			require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
			if result {
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
					cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
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
			d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(string) {}, Rules: func() LaneRules { return LaneRules{} }, ProcessIdentity: ProcessIdentity, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }}
			d.m = Start(time.Now())
			ln := &lane{n: 1, card: &card, t: &turn{started: time.Now().Add(-time.Second)}, session: "session"}
			l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{lanes: []*lane{ln}, state: state, given: map[string]bool{}}}
			now := time.Now()
			l.laneDone(laneResult{ln: ln, t: ln.t, turn: LaneTurn{Exit: exit}, err: err}, now)
			require.NotNil(t, ln.card, "the lane retains its capacity")
			require.NotNil(t, ln.pendingResult, "the returned result is held")
			require.Contains(t, l.lanes.state.Started, job)
			require.NoFileExists(t, card.Report())
			if result {
				require.NotNil(t, l.lanes.state.Started[job].Ending, "the native exit is frozen before a possible daemon restart")
				require.NoFileExists(t, finishNoticePath(d, job))
			}
			require.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			require.True(t, procgroup.ReapVerified(cleanupCtx, pid, "", anchorPath, 100*time.Millisecond))
			cancel()
			l.retryPendingLanes(now.Add(time.Second))
			require.Nil(t, ln.pendingResult)
			if result {
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

//go:build linux

package friend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/require"
)

// boundaryTrace is test evidence, not a production protocol. Each event says
// where its observation came from; a fixture may use simulated row/bus verbs
// or a real isolated server, while the shell group and disk reads are native.
type boundaryTrace struct {
	Seq          int            `json:"seq"`
	Event        string         `json:"event"`
	EvidenceKind string         `json:"evidence_kind"` // native, disk, server, or simulated
	Source       string         `json:"source"`
	Claim        *boundaryClaim `json:"claim,omitempty"`
	Mode         string         `json:"mode,omitempty"`
	Machine      string         `json:"machine,omitempty"`
	Ownership    string         `json:"ownership,omitempty"`
	Started      []string       `json:"started_keys,omitempty"`
	StopOwed     []string       `json:"stop_return_keys,omitempty"`
	PID          int            `json:"pid,omitempty"`
	BirthKnown   bool           `json:"birth_known,omitempty"`
	Runnable     *bool          `json:"runnable,omitempty"`
}

type boundaryClaim struct {
	Job        string `json:"job"`
	Epoch      string `json:"epoch"`
	Generation int    `json:"generation"`
	RunID      string `json:"run_id"`
}

func traceBoundary(t *testing.T, seq *int, e boundaryTrace) {
	t.Helper()
	*seq++
	e.Seq = *seq
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	t.Logf("BOUNDARY_TRACE %s", raw)
}

func boundaryDisk(t *testing.T, stateDir string) ([]string, []string) {
	t.Helper()
	s, err := ReadLanes(stateDir)
	require.NoError(t, err)
	started := make([]string, 0, len(s.Started))
	for job := range s.Started {
		started = append(started, job)
	}
	slices.Sort(started)
	owed := []string{}
	for _, ret := range s.StopReturns {
		if ret.Owed() {
			owed = append(owed, ret.Job)
		}
	}
	slices.Sort(owed)
	return started, owed
}

// boundaryNativeCompletion leaves a genuinely live descendant after the
// leader writes REPORT.md and exits. The anchor is temporarily lost so the
// recovery path cannot infer quiet from a successful leader wait alone.
func boundaryNativeCompletion(t *testing.T, dir, job string, card Card) (int, string, []byte) {
	t.Helper()
	anchorPath := runReceiptPath(dir, job) + ".anchor"
	var anchor []byte
	var anchorErr error
	var lost sync.Once
	pid := 0
	runID := "native-" + job + "-run"
	ctx := WithProcessStarted(context.Background(), func(started int) error {
		pid = started
		return writeRunReceipt(dir, job, runReceipt{RunID: runID, PID: pid, Identity: ProcessIdentity(pid)})
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
	t.Cleanup(func() {
		if len(anchor) > 0 && procgroup.GroupRunnable(pid) {
			_ = os.WriteFile(anchorPath, anchor, 0o600)
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if !procgroup.ReapVerified(cleanupCtx, pid, "", anchorPath, 100*time.Millisecond) {
				t.Errorf("native process group %d was not reaped", pid)
			}
		}
	})
	script := "printf 'Verdict: FAIL\\n\\nNative completed.\\n' > \"$1\"; sleep 30 >/dev/null 2>&1 & echo ready; exit 7"
	_, exit, _ := realExec(ctx, 100*time.Millisecond, dir, "/bin/sh", []string{"-c", script, "sh", card.Report()}, "")
	require.NoError(t, anchorErr)
	require.Equal(t, 7, exit)
	require.True(t, procgroup.GroupRunnable(pid))
	report, err := os.ReadFile(card.Report())
	require.NoError(t, err)
	require.Contains(t, string(report), "Native completed")
	receipt, err := readRunReceipt(dir, job)
	require.NoError(t, err)
	require.Equal(t, runID, receipt.RunID)
	require.Equal(t, pid, receipt.PID)
	require.NotEmpty(t, receipt.Identity)
	return pid, runID, anchor
}

// This drives Daemon.Run after a native shell has written a canonical report,
// exited, and left a child in its original process group. The deliberate
// anchor loss prevents a false quiescence proof. A second durable claim is an
// unlaunched reservation; STOP may return that claim, but never the completed
// native claim. The prior one-shot row had two lanes; the restarted row is
// batch width one. The fixture process ends before Run begins: this is a
// daemon restart boundary, not proof that an OS-killed daemon wrote the files.
func TestCompletionBoundaryNativeRestartTwoClaimsUnderStop(t *testing.T) {
	t.Parallel()
	const aJob, bJob, cJob = "alpha~15.g3", "beta~15.g4", "gamma~15.g2"
	r := newRig(t)
	r.passive = true
	r.d.Width = 1
	stateDir := t.TempDir()
	r.d.StateDir = stateDir
	r.d.Coordinator = "ada"
	r.d.Rules = func() LaneRules { return LaneRules{} }
	r.d.Row = func() (string, int) { return ModeBatch, 1 }
	r.d.ProcessIdentity = ProcessIdentity
	r.d.MachineKnown = func() bool { return true }
	stopped := true
	r.d.MachineStopped = func() bool { return stopped }
	row := &twinRow{}
	a := workCard("alpha", "working")
	a.Job, a.Gen = aJob, 3
	b := workCard("beta", "working")
	b.Job, b.Gen = bJob, 4
	c := workCard("gamma", "working")
	c.Job, c.Gen = cJob, 2
	row.set(a, b, c)
	r.d.Held = row.held
	aCard := Card{ID: a.Card, Outbox: filepath.Join(r.d.Dir, "outbox", aJob)}
	bCard := Card{ID: b.Card, Outbox: filepath.Join(r.d.Dir, "outbox", bJob)}
	require.NoError(t, os.MkdirAll(aCard.Outbox, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(r.d.Dir, "jobs", aJob), 0o755))
	outboxReport(t, r.d.Dir, cJob, "Verdict: FAIL\n\nUnrelated batch report.\n")

	pid, runID, anchor := boundaryNativeCompletion(t, r.d.Dir, aJob, aCard)
	anchorPath := runReceiptPath(r.d.Dir, aJob) + ".anchor"
	receipt, err := readRunReceipt(r.d.Dir, aJob)
	require.NoError(t, err)
	seq := 0
	yes := true
	aClaim := &boundaryClaim{Job: aJob, Epoch: "15", Generation: 3, RunID: runID}
	traceBoundary(t, &seq, boundaryTrace{Event: "native_group_live", EvidenceKind: "native", Source: "process-and-receipt-readback", Claim: aClaim, PID: pid, BirthKnown: true, Runnable: &yes})
	traceBoundary(t, &seq, boundaryTrace{Event: "canonical_report_saved", EvidenceKind: "native", Source: "shell-file-readback", Claim: aClaim})

	state := LaneState{Started: map[string]Started{
		aJob: {Lane: 1, Card: aCard, At: time.Now().Add(-time.Minute), RunID: runID, Pid: pid},
		bJob: {Lane: 2, Card: bCard, At: time.Now().Add(-time.Minute), RunID: "reserved-beta"},
	}}
	require.NoError(t, WriteLanes(stateDir, state))
	started, owed := boundaryDisk(t, stateDir)
	require.Equal(t, []string{aJob, bJob}, started)
	traceBoundary(t, &seq, boundaryTrace{Event: "daemon_restart", EvidenceKind: "simulated", Source: "fixture-state-file-readback", Mode: ModeBatch, Machine: MachineStoppedWord, Ownership: "two prior claims persisted", Started: started, StopOwed: owed})
	r.d.LoadLanes = func() (LaneState, error) {
		loaded, err := ReadLanes(stateDir)
		if err == nil {
			s, o := boundaryDisk(t, stateDir)
			traceBoundary(t, &seq, boundaryTrace{Event: "lane_load_known", EvidenceKind: "disk", Source: "daemon-hook-file-readback", Ownership: "known", Started: s, StopOwed: o})
		}
		return loaded, err
	}
	journalingObserved := false
	r.d.SaveLanes = func(s LaneState) error {
		if err := WriteLanes(stateDir, s); err != nil {
			return err
		}
		saved, owed := boundaryDisk(t, stateDir)
		if !slices.Contains(saved, aJob) && !journalingObserved {
			note, err := os.ReadFile(finishNoticePath(r.d, aJob))
			require.NoError(t, err, "Started clear must follow durable finish journal")
			require.Contains(t, string(note), finishNoticeToken(r.d.Friend, aJob))
			traceBoundary(t, &seq, boundaryTrace{Event: "finish_journal_saved", EvidenceKind: "disk", Source: "file-readback", Claim: aClaim})
			traceBoundary(t, &seq, boundaryTrace{Event: "started_clear_saved", EvidenceKind: "disk", Source: "daemon-hook-file-readback", Claim: aClaim, Started: saved, StopOwed: owed})
			journalingObserved = true
		}
		return nil
	}
	var finished, returned []string
	r.d.Finish = func(_ context.Context, argv []string) error {
		require.GreaterOrEqual(t, len(argv), 4)
		card := argv[3]
		finished = append(finished, card)
		if card == "alpha@3" {
			s, _ := boundaryDisk(t, stateDir)
			require.NotContains(t, s, aJob)
			require.False(t, procgroup.GroupRunnable(pid))
			require.FileExists(t, finishNoticePath(r.d, aJob))
			traceBoundary(t, &seq, boundaryTrace{Event: "outer_finish_requested", EvidenceKind: "simulated", Source: "server-verb-after-native-proof", Claim: aClaim, Started: s})
		} else if card == "gamma@2" {
			traceBoundary(t, &seq, boundaryTrace{Event: "unrelated_report_finish_requested", EvidenceKind: "simulated", Source: "server-verb", Claim: &boundaryClaim{Job: cJob, Epoch: "15", Generation: 2}})
		}
		return nil
	}
	r.d.StopReturn = func(_ context.Context, argv []string) error {
		require.GreaterOrEqual(t, len(argv), 4)
		returned = append(returned, argv[3])
		if argv[3] == "beta@4" {
			s, o := boundaryDisk(t, stateDir)
			traceBoundary(t, &seq, boundaryTrace{Event: "other_claim_stop_return_requested", EvidenceKind: "simulated", Source: "server-verb-and-file-readback", Claim: &boundaryClaim{Job: bJob, Epoch: "15", Generation: 4, RunID: "reserved-beta"}, Started: s, StopOwed: o})
		}
		return nil
	}
	r.at[1] = func() {
		require.Equal(t, ModeBatch, r.d.status.Mode)
		s, o := boundaryDisk(t, stateDir)
		traceBoundary(t, &seq, boundaryTrace{Event: "mode_selected", EvidenceKind: "simulated", Source: "daemon-status-and-file-readback", Mode: r.d.status.Mode, Machine: MachineStoppedWord, Started: s, StopOwed: o})
	}
	r.at[3] = func() {
		require.True(t, procgroup.GroupRunnable(pid), "the native group is still live after daemon restart")
		s, o := boundaryDisk(t, stateDir)
		require.Contains(t, s, aJob, "native group still owns its completion claim")
		require.NotContains(t, returned, "alpha@3")
		require.NotContains(t, finished, "alpha@3")
		require.Contains(t, finished, "gamma@2", "an unrelated batch report may finish")
		traceBoundary(t, &seq, boundaryTrace{Event: "completed_live_retained_under_stop", EvidenceKind: "disk", Source: "daemon-hook-file-readback", Claim: aClaim, Mode: ModeBatch, Machine: MachineStoppedWord, Started: s, StopOwed: o, PID: pid, BirthKnown: true, Runnable: &yes})
		stopped = false
		r.send(t, "ada", "new batch work", "fixture work")
	}
	r.at[5] = func() {
		r.mu.Lock()
		got := len(r.delivered)
		r.mu.Unlock()
		require.Zero(t, got, "batch provider admission remains held by prior native claim")
		s, o := boundaryDisk(t, stateDir)
		require.Contains(t, s, aJob)
		traceBoundary(t, &seq, boundaryTrace{Event: "batch_work_held", EvidenceKind: "simulated", Source: "provider-and-file-readback", Mode: ModeBatch, Machine: "RUNNING", Started: s, StopOwed: o})
		require.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.True(t, procgroup.ReapVerified(cleanupCtx, pid, receipt.Identity, anchorPath, 100*time.Millisecond))
		require.False(t, procgroup.GroupRunnable(pid))
		no := false
		traceBoundary(t, &seq, boundaryTrace{Event: "group_quiet_verified", EvidenceKind: "native", Source: "process-birth-anchor", Claim: aClaim, PID: pid, BirthKnown: true, Runnable: &no})
		// The native report existed before restart. After group proof, a
		// readback makes it eligible for settlement; no second write occurs.
		report, err := os.ReadFile(aCard.Report())
		require.NoError(t, err)
		require.Contains(t, string(report), "Native completed")
		traceBoundary(t, &seq, boundaryTrace{Event: "canonical_report_revalidated", EvidenceKind: "disk", Source: "post-quiet-file-readback", Claim: aClaim})
	}
	r.run(t, 9)
	require.NotContains(t, returned, "alpha@3", "completed work cannot become a STOP return")
	require.Contains(t, returned, "beta@4", "the second, unlaunched claim is returned independently")
	finishCount := 0
	for _, card := range finished {
		if card == "alpha@3" {
			finishCount++
		}
	}
	require.Equal(t, 1, finishCount, "native completion finishes once")
	saved, owed := boundaryDisk(t, stateDir)
	require.NotContains(t, saved, aJob)
	require.NotContains(t, owed, aJob)
	traceBoundary(t, &seq, boundaryTrace{Event: "final_disk_readback", EvidenceKind: "disk", Source: "file-readback", Started: saved, StopOwed: owed})
}

// A decoder may return apparently valid claims along with an error. This is
// intentionally a simulated read failure; it checks that the daemon never
// writes those partial fields over the two real persisted claims.
func TestCompletionBoundaryPartialReadKeepsBothDiskClaims(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.passive = true
	r.d.Row = func() (string, int) { return ModeBatch, 1 }
	r.d.MachineKnown = func() bool { return true }
	stateDir := t.TempDir()
	r.d.StateDir = stateDir
	const aJob, bJob = "alpha~15.g3", "beta~15.g4"
	a := Card{ID: "alpha", Outbox: filepath.Join(r.d.Dir, "outbox", aJob)}
	b := Card{ID: "beta", Outbox: filepath.Join(r.d.Dir, "outbox", bJob)}
	state := LaneState{Started: map[string]Started{aJob: {Lane: 1, Card: a, RunID: "a"}, bJob: {Lane: 2, Card: b, RunID: "b"}}}
	require.NoError(t, WriteLanes(stateDir, state))
	seq := 0
	s, o := boundaryDisk(t, stateDir)
	traceBoundary(t, &seq, boundaryTrace{Event: "before_partial_load", EvidenceKind: "disk", Source: "file-readback", Started: s, StopOwed: o})
	r.d.LoadLanes = func() (LaneState, error) {
		traceBoundary(t, &seq, boundaryTrace{Event: "lane_load_partial_error", EvidenceKind: "simulated", Source: "read-fault", Started: []string{aJob}})
		return LaneState{Started: map[string]Started{aJob: state.Started[aJob]}}, errors.New("injected partial read")
	}
	writes := 0
	r.d.SaveLanes = func(s LaneState) error { writes++; return WriteLanes(stateDir, s) }
	r.send(t, "ada", "new batch work", "fixture work")
	r.run(t, 4)
	s, o = boundaryDisk(t, stateDir)
	require.Equal(t, []string{aJob, bJob}, s)
	require.Zero(t, writes)
	r.mu.Lock()
	got := len(r.delivered)
	r.mu.Unlock()
	require.Zero(t, got)
	traceBoundary(t, &seq, boundaryTrace{Event: "after_partial_load", EvidenceKind: "disk", Source: "file-readback-and-simulated-provider", Started: s, StopOwed: o})
}

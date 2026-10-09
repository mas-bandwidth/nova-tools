package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResultRecoveryDoesNotInventExitAfterFreezeWriteFailure(t *testing.T) {
	t.Parallel()
	dir, stateDir := laneEndFixture(t, "c2~15"), t.TempDir()
	card := Card{ID: "c2", Brief: filepath.Join(dir, "inbox", "c2~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c2~15")}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	require.NoError(t, os.WriteFile(card.Result(), []byte("RESULT: native completion\n"), 0o644))
	state := LaneState{Started: map[string]Started{"c2~15": {Lane: 1, Card: card, At: t0.Add(-time.Minute)}}}
	require.NoError(t, WriteLanes(stateDir, state))
	fail := true
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Record: func(string) {}, SaveLanes: func(s LaneState) error {
		if fail {
			fail = false
			return errors.New("state write refused")
		}
		return WriteLanes(stateDir, s)
	}}
	l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{state: state, given: map[string]bool{}}}
	words := l.endCard(1, card, LaneEnd{Exit: 7, Wall: time.Minute, NoReport: true, Started: state.Started["c2~15"].At}, t0)
	assert.Contains(t, words, "report_debt_error=")
	assert.NoFileExists(t, card.Report())
	saved, err := ReadLanes(stateDir)
	require.NoError(t, err)
	require.Nil(t, saved.Started["c2~15"].Ending)
	restarted := &loop{d: d, ctx: context.Background(), lanes: &laneSet{state: saved, given: map[string]bool{}}}
	restarted.endStarted(t0.Add(time.Minute))
	raw, err := os.ReadFile(card.Report())
	require.NoError(t, err)
	assert.Contains(t, string(raw), "the run's exit and wall are unavailable")
	assert.NotContains(t, string(raw), "exit 0")
	assert.NotContains(t, string(raw), "wall 0s")
	assert.NotContains(t, string(raw), "the run is gone")
	assert.Empty(t, restarted.lanes.state.Started)
	assert.Empty(t, restarted.lanes.state.GivenUp)
}

// TestResultCompletionSurvivesReportWriteFailureAndRestart pins the report
// publication boundary: a durable native result must keep its completion debt
// until its canonical report is durable, and restart replays that same end.
func TestResultCompletionSurvivesReportWriteFailureAndRestart(t *testing.T) {
	t.Parallel()
	dir := laneEndFixture(t, "c1~15")
	card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	result := []byte("RESULT: completed native work\n")
	require.NoError(t, os.WriteFile(card.Result(), result, 0o644))
	// A fixture-only symlink parent injects atomicfile's write refusal without
	// depending on uid or permissions; RESULT remains readable throughout.
	realOut := card.Outbox + ".real"
	require.NoError(t, os.Rename(card.Outbox, realOut))
	require.NoError(t, os.Symlink(realOut, card.Outbox))
	r, saved := laneRig(t, &lanesHarness{dir: dir, active: map[string]int{}}, 1)
	state := LaneState{Sessions: map[int]string{1: "ses_1"}, Started: map[string]Started{
		"c1~15": {Lane: 1, Card: card, At: t0.Add(-time.Minute)},
	}}
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{state: state, given: map[string]bool{}}}
	finish := &finishes{}
	r.d.Finish = finish.finish
	words := l.endCard(1, card, LaneEnd{Exit: 0, NoReport: true, Wall: time.Minute, Started: state.Started["c1~15"].At}, t0)
	assert.Contains(t, words, "report_error=", "the injected canonical-report write fails")
	assert.NotEmpty(t, saved.Started, "report write failure retains durable completion debt")
	assert.Empty(t, saved.GivenUp, "completed native work is not abandoned")
	assert.Empty(t, finish.got(), "no sprint disposition before canonical report publication")
	raw, err := os.ReadFile(card.Result())
	require.NoError(t, err)
	assert.Equal(t, result, raw)
	_, found, err := NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	assert.False(t, found, "NextCard skips RESULT, so completion recovery must own retry")
	// Repair only the test fixture's write boundary, then reload the exact saved
	// state as a new daemon would. No harness turn may be needed to finish it.
	require.NoError(t, os.Remove(card.Outbox))
	require.NoError(t, os.Rename(realOut, card.Outbox))
	restarted := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{state: *saved, given: map[string]bool{}}}
	restarted.endStarted(t0.Add(time.Minute))
	report, err := os.ReadFile(card.Report())
	if assert.NoError(t, err, "restart retries the original report debt") {
		assert.Contains(t, string(report), "the run exited 0 with RESULT.md and no REPORT.md")
		assert.NotContains(t, string(report), "the run is gone", "restart does not reinterpret a completed run")
		assert.True(t, strings.HasPrefix(string(report), "Verdict: FAIL\n"), "existing no-push report policy remains; RESULT does not invent LAND")
	}
	assert.Empty(t, saved.Started, "completion debt clears after its report is durable")
	assert.Empty(t, saved.GivenUp, "recovered completion is not restart abandonment")
	assert.Len(t, finish.got(), 1)
}

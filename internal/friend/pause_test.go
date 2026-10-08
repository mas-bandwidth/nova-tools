package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPausedFriendAndNativeReaderSettleWithoutRefillOrCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}, finish: map[string]bool{"c1": true, "c2": true, "c3": true}, block: make(chan struct{})}, rblock: make(chan struct{}), verdicts: map[string]string{"a.w1": okResult, "b.w1": strings.ReplaceAll(okResult, "aaa", "bbb")}}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 3)
		var paused atomic.Bool
		var returned atomic.Int32
		r.d.StopReturn = func(context.Context, []string) error { returned.Add(1); return nil }
		r.d.MachinePaused = paused.Load
		r.at[10] = func() {
			turns, _, _ := h.got()
			require.Len(t, turns, 1)
			require.Len(t, sp.verbs("--begin"), 2)
			paused.Store(true)
		}
		r.at[15] = func() { close(h.block); close(h.rblock) }
		r.at[25] = func() {
			turns, _, _ := h.got()
			assert.Len(t, turns, 1, "new work holds after the current work and reads exit")
			assert.Len(t, sp.verbs("--begin"), 2)
			assert.Len(t, sp.verbs("--ok"), 2, "current reads settle without cancellation")
			assert.Empty(t, sp.verbs("--return"))
			assert.Zero(t, returned.Load())
			paused.Store(false)
		}
		r.run(t, 45)
		turns, _, _ := h.got()
		assert.Len(t, turns, 3, "unpause resumes the existing queued work")
		assert.NotContains(t, strings.Join(r.records, "\n"), "cancelled by the machine's stop")
	})
}

// The harness waits behind the native hook; successful begin alone is not launch admission.
type heldReadHarness struct {
	*readHarness
	entered, release chan struct{}
	once             atomic.Bool
	hold             func() bool
}

func (h *heldReadHarness) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	if h.once.CompareAndSwap(false, true) {
		close(h.entered)
		<-h.release
	}
	if h.hold() {
		if err := processStarted(ctx)(0); err != nil {
			return LaneTurn{}, err
		}
	}
	return h.readHarness.RunRead(ctx, model, prompt)
}
func TestNativeReaderFreshAdmissionRetainsBegunLeaseThroughPause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult, "b.w1": strings.ReplaceAll(okResult, "aaa", "bbb")}}
		gate := &heldReadHarness{readHarness: h, entered: make(chan struct{}), release: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 1)
		r.d.Deliver = gate
		var serverPaused atomic.Bool
		// Cached beat is deliberately RUNNING; only the final fresh callback sees pause.
		r.d.LaunchAllowed = func() bool { return true }
		r.d.AdmitLaunch = func(context.Context) bool { return !serverPaused.Load() }
		gate.hold = serverPaused.Load
		r.at[3] = func() { <-gate.entered; serverPaused.Store(true); close(gate.release) }
		r.at[8] = func() {
			assert.Empty(t, h.prompts, "the native gate refuses the model start")
			require.Len(t, sp.verbs("--begin"), 1)
			assert.Empty(t, sp.verbs("--return"))
			a, found, err := loadReadActive(filepath.Join(dir, "reads", "a.w1"))
			require.NoError(t, err)
			require.True(t, found)
			assert.True(t, a.LaunchHeld)
			assert.Zero(t, a.PID)
			// Unpause resumes this held lease without beginning it again.
			r.d.MachinePaused = serverPaused.Load
			serverPaused.Store(false)
		}
		r.run(t, 30)
		assert.Len(t, sp.verbs("--begin"), 2, "each canonical lease is begun once")
		assert.Len(t, h.prompts, 2, "the same begun lease resumes after fresh RUNNING")
		assert.Len(t, sp.verbs("--ok"), 2)
		assert.Empty(t, sp.verbs("--return"))
	})
}
func TestWorkNativeGateFreshlyRefusesPauseWithoutReplacingReservation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "job"), 0755))
	var paused atomic.Bool
	paused.Store(true)
	st := Started{RunID: "same-run", Card: Card{ID: "c1"}}
	l := &loop{ctx: context.Background(), lanes: &laneSet{state: LaneState{Started: map[string]Started{"job": st}}}, d: &Daemon{Dir: dir, LaunchAllowed: func() bool { return true }, AdmitLaunch: func(context.Context) bool { return !paused.Load() }, ProcessIdentity: func(int) string { return "birth" }}}
	gate := l.pidSink("job")
	require.ErrorIs(t, gate(4242), errMachineLaunchHeld)
	assert.Equal(t, st, l.lanes.state.Started["job"])
	_, err := os.Stat(runReceiptPath(dir, "job"))
	assert.True(t, os.IsNotExist(err))
	paused.Store(false)
	require.NoError(t, gate(4242))
	assert.Equal(t, st.RunID, l.lanes.state.Started["job"].RunID)
	if ProcessIdentitySupported {
		receipt, err := readRunReceipt(dir, "job")
		require.NoError(t, err)
		assert.Equal(t, st.RunID, receipt.RunID)
		assert.Equal(t, "birth", receipt.Identity)
	}
}
func TestPausedRestartKeepsNeverLaunchedWorkReservationUntilUnpause(t *testing.T) {
	t.Parallel()
	pausedWorkRestart(t, false)
}
func TestPausedRestartKeepsQuiescentPriorTurnReservationUntilUnpause(t *testing.T) {
	t.Parallel()
	pausedWorkRestart(t, true)
}
func pausedWorkRestart(t *testing.T, previousTurn bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, finish: map[string]bool{"c1": true}}
		r, state := laneRig(t, h, 1)
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		old := "bob lane 1 (daemon 99999998.1)"
		*state = LaneState{Started: map[string]Started{"c1~15": {Lane: 1, Card: card, At: t0, RunID: "held-run", Owner: old, LaunchHeld: true}}}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c1~15"), 0755))
		require.NoError(t, os.WriteFile(laneMarkPath(dir, "c1~15"), []byte(LaneMarkRunningRun(old, t0, "held-run")), 0644))
		if previousTurn {
			st := state.Started["c1~15"]
			st.Pid = 2147483647
			st.LaunchHeld = false
			state.Started["c1~15"] = st
			require.NoError(t, writeRunReceipt(dir, "c1~15", runReceipt{RunID: st.RunID, PID: st.Pid, Identity: "exited-prior-turn"}))
		}
		var paused atomic.Bool
		paused.Store(true)
		r.d.MachinePaused = paused.Load
		r.d.ProcessIdentity = func(int) string { return "" }
		r.d.ProcessAlive = func(int) bool { return false }
		r.at[5] = func() {
			turns, _, _ := h.got()
			assert.Empty(t, turns)
			assert.NoFileExists(t, card.Report())
			require.Contains(t, state.Started, "c1~15")
			assert.Equal(t, "held-run", state.Started["c1~15"].RunID)
			mark, ok := ReadLaneMark(dir, "c1~15")
			require.True(t, ok)
			assert.Equal(t, old, mark.Who)
			paused.Store(false)
		}
		r.run(t, 15)
		turns, _, _ := h.got()
		assert.Len(t, turns, 1)
		assert.NotContains(t, strings.Join(r.records, "\n"), "gone: FAIL")
		assert.FileExists(t, card.Report())
	})
}

func TestRestartedReaderKeepsDurablePausedLeaseAndResumesItWithoutNewBegin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		asked, err := ParseReadQueue(askedQueue)
		require.NoError(t, err)
		require.NotEmpty(t, asked)
		old := asked[0]
		old.Col = "reading"
		active := readActive{Read: old, LaunchHeld: true}
		readDir := filepath.Join(dir, "reads", old.ID)
		require.NoError(t, os.MkdirAll(readDir, 0755))
		require.NoError(t, saveReadActive(readDir, active))
		before, err := os.ReadFile(readActivePath(readDir))
		require.NoError(t, err)
		// This newly constructed daemon/loop has no old running maps, results or callbacks.
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult, "b.w1": strings.ReplaceAll(okResult, "aaa", "bbb")}}
		sp := &readSprint{queue: askedQueue, state: map[string]string{old.ID: "begun"}}
		r := readRig(t, h, sp, 1)
		var paused atomic.Bool
		paused.Store(true)
		r.d.MachinePaused = paused.Load
		r.at[5] = func() {
			assert.Empty(t, h.prompts)
			assert.Empty(t, sp.verbs("--begin"))
			assert.Empty(t, sp.verbs("--return"))
			after, err := os.ReadFile(readActivePath(readDir))
			require.NoError(t, err)
			assert.Equal(t, before, after, "pause recovery changes neither receipt nor lease")
			paused.Store(false)
		}
		r.run(t, 25)
		require.Len(t, h.prompts, 2)
		begins := sp.verbs("--begin")
		require.Len(t, begins, 1, "only the other asked read needs a new begin")
		assert.NotContains(t, begins[0], old.ID+"@"+old.Epoch)
		assert.Len(t, sp.verbs("--ok"), 2)
		assert.Empty(t, sp.verbs("--return"))
	})
}

func TestHeldFreshWorkAdmissionsRetryToConfiguredWidthWithoutUnpause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, finish: map[string]bool{"c1": true, "c2": true, "c3": true}, pid: 4242, block: make(chan struct{})}
		r, _ := laneRig(t, h, 3)
		var attempts atomic.Int32
		r.d.AdmitLaunch = func(context.Context) bool { return attempts.Add(1) > 2 }
		r.d.ProcessIdentity = func(int) string { return "birth" }
		r.at[8] = func() {
			turns, _, _ := h.got()
			assert.Len(t, turns, 3, "held fresh responses retry automatically while RUNNING")
			close(h.block)
		}
		r.run(t, 15)
		turns, _, _ := h.got()
		assert.Len(t, turns, 3)
		assert.GreaterOrEqual(t, attempts.Load(), int32(5))
		assert.NotContains(t, strings.Join(r.records, "\n"), "gone: FAIL")
	})
}

func TestPausedHeldWorkRecoveryRetainsUnprovedPositiveRunBinding(t *testing.T) {
	t.Parallel()
	for _, why := range []string{"different-run", "reused-pid", "unknown-identity"} {
		t.Run(why, func(t *testing.T) {
			t.Parallel()
			dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
			card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
			st := Started{Lane: 1, Card: card, Pid: 2147483647, RunID: "held-run", Owner: "old", LaunchHeld: true}
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c1~15"), 0755))
			receipt := runReceipt{RunID: st.RunID, PID: st.Pid, Identity: "old-birth"}
			identity := func(int) string { return "" }
			switch why {
			case "different-run":
				receipt.RunID = "other-run"
			case "reused-pid":
				identity = func(int) string { return "foreign-birth" }
			case "unknown-identity":
				identity = nil
			}
			require.NoError(t, writeRunReceipt(dir, "c1~15", receipt))
			require.NoError(t, os.WriteFile(laneMarkPath(dir, "c1~15"), []byte(LaneMarkRunningRun(st.Owner, t0, st.RunID)), 0644))
			l := &loop{ctx: context.Background(), d: &Daemon{Dir: dir, MachinePaused: func() bool { return true }, ProcessIdentity: identity, Record: func(string) {}, SaveLanes: func(LaneState) error { return nil }}, lanes: &laneSet{given: map[string]bool{}, state: LaneState{Started: map[string]Started{"c1~15": st}}}}
			l.endStarted(t0)
			assert.Equal(t, st, l.lanes.state.Started["c1~15"])
			assert.True(t, l.lanes.recoveryPending)
			assert.NoFileExists(t, card.Report())
			assert.Empty(t, l.lanes.given)
			mark, found := ReadLaneMark(dir, "c1~15")
			require.True(t, found)
			assert.Equal(t, st.Owner, mark.Who)
		})
	}
}

func TestFailedPausedRetryMarkerSaveRetriesAndSurvivesRunningRestart(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
	card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
	st := Started{Lane: 1, Card: card, Pid: 2147483647, RunID: "held-run", Owner: "bob lane 1 (daemon 99999998.1)"}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c1~15"), 0755))
	require.NoError(t, writeRunReceipt(dir, "c1~15", runReceipt{RunID: st.RunID, PID: st.Pid, Identity: "exited"}))
	require.NoError(t, os.WriteFile(laneMarkPath(dir, "c1~15"), []byte(LaneMarkRunningRun(st.Owner, t0, st.RunID)), 0644))
	calls := 0
	var persisted Started
	d := &Daemon{Friend: "bob", Dir: dir, Record: func(string) {}, ProcessIdentity: func(int) string { return "" }, ProcessAlive: func(int) bool { return false }, SaveLanes: func(s LaneState) error {
		calls++
		if calls == 1 {
			return errors.New("save unavailable")
		}
		persisted = s.Started["c1~15"]
		return nil
	}}
	l := &loop{ctx: context.Background(), d: d, lanes: &laneSet{state: LaneState{Started: map[string]Started{"c1~15": st}}}}
	l.markLaunchHeld("c1~15", t0)
	assert.True(t, l.lanes.heldDirty)
	assert.True(t, l.lanes.state.Started["c1~15"].LaunchHeld)
	assert.False(t, persisted.LaunchHeld)
	l.markLaunchHeld("c1~15", t0)
	assert.False(t, l.lanes.heldDirty)
	require.True(t, persisted.LaunchHeld)
	assert.Equal(t, 2, calls)
	restarted := &loop{ctx: context.Background(), tag: "new", d: d, lanes: &laneSet{given: map[string]bool{}, state: LaneState{Sessions: map[int]string{}, Started: map[string]Started{"c1~15": persisted}}}}
	restarted.endStarted(t0)
	require.Len(t, restarted.lanes.lanes, 1)
	require.NotNil(t, restarted.lanes.lanes[0].card)
	assert.Equal(t, st.RunID, restarted.lanes.state.Started["c1~15"].RunID)
	assert.NoFileExists(t, card.Report())
}

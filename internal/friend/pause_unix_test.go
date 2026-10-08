//go:build unix

package friend

import (
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The inherited reader loop has no in-memory ownership of the old startup group.
// Its durable identity must keep that lease until the live group has exited.
func TestPausedNativeReaderRecoveryWaitsForPositivePIDGroupThenResumesSameLease(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, nil, nil, nil)
	asked, err := ParseReadQueue(askedQueue)
	require.NoError(t, err)
	old := asked[0]
	old.Col = "reading"
	cmd := exec.CommandContext(context.Background(), "/bin/sleep", "30")
	ownGroup(cmd)
	require.NoError(t, cmd.Start())
	waited := false
	defer func() {
		killGroup(cmd.Process.Pid)
		if !waited {
			require.Error(t, cmd.Wait())
		}
	}()
	readDir := filepath.Join(dir, "reads", old.ID)
	active := readActive{Read: old, PID: cmd.Process.Pid, Identity: ProcessIdentity(cmd.Process.Pid), LaunchHeld: true}
	require.NotEmpty(t, active.Identity)
	require.NoError(t, os.MkdirAll(readDir, 0755))
	require.NoError(t, saveReadActive(readDir, active))
	var paused atomic.Bool
	paused.Store(true)
	h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{old.ID: okResult}}
	sp := &readSprint{queue: askedQueue, state: map[string]string{old.ID: "begun"}}
	records := []string{}
	d := &Daemon{Friend: "bob", Harness: "fake", Dir: dir, Record: func(v string) { records = append(records, v) }, MachinePaused: paused.Load, Sprint: sp.ask, ReadSlots: func() int { return 1 }, Deliver: h}
	l := &loop{ctx: context.Background(), d: d, reads: newReadSet(), lanes: &laneSet{width: 1, state: LaneState{Started: map[string]Started{}}}}
	l.readStep(time.Now())
	assert.True(t, procgroup.GroupRunnable(cmd.Process.Pid), "pause recovery never signals the existing group")
	assert.Empty(t, h.prompts)
	assert.Empty(t, sp.verbs("--begin"))
	assert.Empty(t, sp.verbs("--return"))
	paused.Store(false)
	l.readStep(time.Now().Add(ReadAskEvery))
	assert.Empty(t, h.prompts, "RUNNING cannot replace the still-runnable held native group")
	assert.Empty(t, l.reads.running, "no replacement native turn is admitted while old group can run")
	live, liveFound, liveErr := loadReadActive(readDir)
	require.NoError(t, liveErr)
	require.True(t, liveFound, strings.Join(records, "\n"))
	require.Equal(t, active, live)
	assert.Empty(t, sp.verbs("--begin"))
	assert.Empty(t, sp.verbs("--return"))
	assert.True(t, procgroup.GroupRunnable(cmd.Process.Pid))
	paused.Store(true)
	killGroup(cmd.Process.Pid)
	require.Error(t, cmd.Wait())
	waited = true
	require.False(t, procgroup.GroupRunnable(cmd.Process.Pid))
	l.readStep(time.Now().Add(ReadAskEvery))
	saved, found, err := loadReadActive(readDir)
	require.NoError(t, err)
	require.True(t, found, strings.Join(records, "\n"))
	assert.Equal(t, active, saved, "quiescence during pause preserves the receipt")
	paused.Store(false)
	l.readStep(time.Now().Add(2 * ReadAskEvery))
	result := <-l.reads.results
	l.readDone(result, time.Now())
	assert.Len(t, h.prompts, 1)
	assert.Empty(t, sp.verbs("--begin"))
	assert.Len(t, sp.verbs("--ok"), 1)
	assert.Empty(t, sp.verbs("--return"))
}

type nativePausedReadHarness struct {
	*lanesHarness
	entered, release chan struct{}
	first            atomic.Bool
}

func (h *nativePausedReadHarness) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	if h.first.CompareAndSwap(false, true) {
		close(h.entered)
		<-h.release
	}
	dir := readDirOfPrompt.FindStringSubmatch(prompt)[1]
	_, exit, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "printf yes > native-started"}, "")
	if err == nil {
		text := okResult
		if filepath.Base(dir) == "b.w1" {
			text = strings.ReplaceAll(text, "aaa", "bbb")
		}
		err = os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte(text), 0644)
	}
	return LaneTurn{Exit: exit}, err
}
func TestNativeReadPauseAtRealProcessGateHoldsLeaseAndResumesWithoutRebegin(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, nil, nil, nil)
	h := &nativePausedReadHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, entered: make(chan struct{}), release: make(chan struct{})}
	asked, err := ParseReadQueue(askedQueue)
	require.NoError(t, err)
	r := asked[0]
	r.Col = "reading"
	sp := &readSprint{queue: askedQueue, state: map[string]string{r.ID: "begun"}}
	var paused atomic.Bool
	d := &Daemon{Friend: "bob", Harness: "fake", Dir: dir, Record: func(string) {}, LaunchAllowed: func() bool { return true }, AdmitLaunch: func(context.Context) bool { return !paused.Load() }, Sprint: sp.ask, ReadSlots: func() int { return 1 }, Deliver: h}
	l := &loop{ctx: context.Background(), d: d, reads: newReadSet(), lanes: &laneSet{width: 1, state: LaneState{Started: map[string]Started{}}}}
	l.reads.running[r.ID] = true
	l.reads.begun[r.ID] = true
	l.startRead(r, time.Now())
	<-h.entered
	paused.Store(true)
	close(h.release)
	result := <-l.reads.results
	require.ErrorIs(t, result.err, errMachineLaunchHeld)
	l.readDone(result, time.Now())
	readDir := filepath.Join(dir, "reads", r.ID)
	assert.NoFileExists(t, filepath.Join(readDir, "native-started"))
	active, found, err := loadReadActive(readDir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Positive(t, active.PID)
	assert.True(t, active.LaunchHeld)
	assert.Empty(t, ProcessIdentity(active.PID), "the refused gated leader was waited before completion")
	require.Eventually(t, func() bool { return !procgroup.GroupRunnable(active.PID) }, 30*time.Second, 20*time.Millisecond, "observe group quiescence before retry; transient unrelated /proc reads fail closed")
	assert.Empty(t, sp.verbs("--return"))
	paused.Store(false)
	l.reads.running[r.ID] = true
	l.startRead(r, time.Now())
	result = <-l.reads.results
	require.NoError(t, result.err)
	l.readDone(result, time.Now())
	assert.FileExists(t, filepath.Join(readDir, "native-started"))
	assert.Empty(t, sp.verbs("--begin"))
	assert.Len(t, sp.verbs("--ok"), 1)
}

func TestNativeReadSupersededAdmissionsRefillSameLeaseWithoutUnpause(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, nil, nil, nil)
	h := &nativePausedReadHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, entered: make(chan struct{}), release: make(chan struct{})}
	close(h.release)
	asked, err := ParseReadQueue(askedQueue)
	require.NoError(t, err)
	r := asked[0]
	r.Col = "reading"
	sp := &readSprint{queue: askedQueue, state: map[string]string{r.ID: "begun"}}
	var attempts atomic.Int32
	d := &Daemon{Friend: "bob", Harness: "fake", Dir: dir, Record: func(string) {}, LaunchAllowed: func() bool { return true }, AdmitLaunch: func(context.Context) bool { return attempts.Add(1) > 2 }, Sprint: sp.ask, ReadSlots: func() int { return 1 }, Deliver: h}
	l := &loop{ctx: context.Background(), d: d, reads: newReadSet(), lanes: &laneSet{width: 1, state: LaneState{Started: map[string]Started{}}}}
	for n := 0; n < 3; n++ {
		if n == 0 {
			l.reads.running[r.ID] = true
			l.reads.begun[r.ID] = true
			l.startRead(r, time.Now())
		} else {
			l.readStep(time.Now().Add(time.Duration(n) * ReadAskEvery))
		}
		result := <-l.reads.results
		if n < 2 {
			require.ErrorIs(t, result.err, errMachineLaunchHeld)
		} else {
			require.NoError(t, result.err)
		}
		l.readDone(result, time.Now())
		if n < 2 {
			assert.NoFileExists(t, filepath.Join(dir, "reads", r.ID, "native-started"))
			assert.Empty(t, sp.verbs("--return"))
		}
	}
	assert.Equal(t, int32(3), attempts.Load())
	assert.Len(t, sp.verbs("--ok"), 1)
	assert.Empty(t, sp.verbs("--begin"))
	assert.Empty(t, sp.verbs("--return"))
}

func TestHeldWorkRecoveryDoesNotResumeUntilNativePriorGroupIsGone(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
	cmd := exec.CommandContext(context.Background(), "/bin/sleep", "30")
	ownGroup(cmd)
	require.NoError(t, cmd.Start())
	waited := false
	defer func() {
		killGroup(cmd.Process.Pid)
		if !waited {
			require.Error(t, cmd.Wait())
		}
	}()
	card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
	old := "bob lane 1 (daemon 99999998.1)"
	st := Started{Lane: 1, Card: card, At: t0, Pid: cmd.Process.Pid, RunID: "held-run", Owner: old, LaunchHeld: true}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c1~15"), 0755))
	require.NoError(t, writeRunReceipt(dir, "c1~15", runReceipt{RunID: st.RunID, PID: st.Pid, Identity: ProcessIdentity(st.Pid)}))
	require.NoError(t, os.WriteFile(laneMarkPath(dir, "c1~15"), []byte(LaneMarkRunningRun(old, t0, st.RunID)), 0644))
	// The leader's identity is unavailable while descendants may still execute.
	// That absence alone is never permission to launch a replacement turn.
	d := &Daemon{Friend: "bob", Dir: dir, MachinePaused: func() bool { return false }, ProcessIdentity: func(int) string { return "" }, ProcessAlive: func(int) bool { return false }, Record: func(string) {}, SaveLanes: func(LaneState) error { return nil }}
	l := &loop{ctx: context.Background(), tag: "current", d: d, lanes: &laneSet{given: map[string]bool{}, state: LaneState{Sessions: map[int]string{}, Started: map[string]Started{"c1~15": st}}}}
	l.endStarted(t0)
	assert.Len(t, l.lanes.adopt, 1, "a live old group is watched instead of rebuilding its continuation")
	assert.Empty(t, l.lanes.lanes, "no new native turn is prepared while the old group can run")
	assert.True(t, procgroup.GroupRunnable(st.Pid))
	assert.NoFileExists(t, card.Report())
	killGroup(st.Pid)
	require.Error(t, cmd.Wait())
	waited = true
	require.False(t, procgroup.GroupRunnable(st.Pid))
	// A genuinely new owner can now recover the same held reservation.
	restarted := &loop{ctx: context.Background(), tag: "new", d: d, lanes: &laneSet{given: map[string]bool{}, state: LaneState{Sessions: map[int]string{}, Started: map[string]Started{"c1~15": st}}}}
	restarted.endStarted(t0)
	require.Len(t, restarted.lanes.lanes, 1)
	require.NotNil(t, restarted.lanes.lanes[0].card)
	assert.Equal(t, card.ID, restarted.lanes.lanes[0].card.ID)
	assert.Equal(t, st.RunID, restarted.lanes.state.Started["c1~15"].RunID)
	assert.NoFileExists(t, card.Report())
}

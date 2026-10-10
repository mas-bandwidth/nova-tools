package friend

import (
	"context"
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

// The machine's stop cancels jobs (stop.go; tla/StopCancels.tla). These tests are the model's
// traces on the daemon's lanes: a turn under way when the word turns STOPPED is cancelled,
// its card leaves the lane never finished and is handed back with stop-return once the run
// has ended (EveryLaneReturnsOnStop); no turn begins while STOPPED (NoLaunchAfterStop,
// NoLaneWhileStopped); a stop-return owed survives a daemon restart and is sent first, never
// turned into a FAIL (StopReturnsSurviveRestart); no work nudge goes into the session while
// STOPPED (NoNudgeWhileStopped).

func TestParseMachineReadsTheWordOffTheBeatsAnswer(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		answer string
		state  string
		ok     bool
	}{
		{"FRIEND-BEAT OK bob at=2026-10-08T16:00:00Z row_mode=one-shot row_width=4 machine=STOPPED", "STOPPED", true},
		{"FRIEND-BEAT OK bob at=2026-10-08T16:00:00Z working=2 machine=RUNNING", "RUNNING", true},
		{"FRIEND-BEAT OK bob at=2026-10-08T16:00:00Z row_mode=batch", "", false},
	} {
		st, ok := ParseMachine(c.answer)
		assert.Equal(t, c.ok, ok, c.answer)
		assert.Equal(t, c.state, st, c.answer)
	}
	assert.Equal(t, []string{"stop-return", "--as", "friend.bob", "c1@3", "--epoch", "15", "--reason", "owned process stopped"}, StopReturnArgv("friend.bob", "c1", 3, "15"))
}

// stopWordHarness blocks every card's first turn until its context ends (the stop's cancel),
// and runs the second as the plain harness does (the card done).
type stopWordHarness struct {
	*lanesHarness
	mu    sync.Mutex
	calls map[string]int
	ended map[string]bool // the first turn ended by the daemon, not by itself
}

func (h *stopWordHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	id := cardOfText.FindStringSubmatch(text)[1]
	h.mu.Lock()
	h.calls[id]++
	n := h.calls[id]
	h.mu.Unlock()
	if n == 1 {
		<-ctx.Done()
		h.mu.Lock()
		h.ended[id] = true
		h.mu.Unlock()
		return LaneTurn{Exit: -1}, ctx.Err()
	}
	return h.lanesHarness.DeliverTo(ctx, session, text)
}

// stopRig is a lane rig whose machine word the test flips, and whose stop-returns are
// recorded.
type stopRig struct {
	*rig
	mu      sync.Mutex
	stopped bool
	returns []string
	refuse  error
}

func newStopRig(t *testing.T, h *lanesHarness, width int) (*stopRig, *LaneState) {
	t.Helper()
	r, state := laneRig(t, h, width)
	sr := &stopRig{rig: r}
	r.d.MachineStopped = func() bool { sr.mu.Lock(); defer sr.mu.Unlock(); return sr.stopped }
	r.d.StopReturn = func(_ context.Context, argv []string) error {
		sr.mu.Lock()
		defer sr.mu.Unlock()
		sr.returns = append(sr.returns, strings.Join(argv, " "))
		return sr.refuse
	}
	return sr, state
}

func (r *stopRig) set(stopped bool) { r.mu.Lock(); r.stopped = stopped; r.mu.Unlock() }

func (r *stopRig) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.returns...)
}

// Trace: two lanes run c1 and c2; the word turns STOPPED; both turns are cancelled, each card
// leaves its lane never finished, each is owed and then handed back with stop-return once its
// run ended; nothing starts while STOPPED; RUNNING again runs the cards the queue still names.
func TestStoppedCancelsEveryLaneAndHandsEachCardBackWithStopReturn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
		h := &stopWordHarness{lanesHarness: lh, calls: map[string]int{}, ended: map[string]bool{}}
		r, state := newStopRig(t, lh, 2)
		r.d.Deliver = h
		var atStop map[string]int
		r.at[12] = func() { r.set(true) }
		r.at[40] = func() {
			h.mu.Lock()
			atStop = map[string]int{"c1": h.calls["c1"], "c2": h.calls["c2"]}
			h.mu.Unlock()
			r.set(false)
		}
		r.run(t, 80)

		records := strings.Join(r.records, "\n")
		underStop, _, _ := strings.Cut(records, "machine RUNNING") // the record up to the resume
		h.mu.Lock()
		defer h.mu.Unlock()
		assert.True(t, h.ended["c1"] && h.ended["c2"], "both turns under way were ended by the daemon: %s", records)
		assert.Equal(t, map[string]int{"c1": 1, "c2": 1}, atStop, "no turn began while STOPPED (NoLaunchAfterStop)")
		assert.Regexp(t, `machine STOPPED: every lane and read under way is cancelled`, records)
		assert.Regexp(t, `lane \d: card c1@1 cancelled by stop: its process group is told to end; the job directory and the branch are kept; stop-return owed`, records)
		assert.Regexp(t, `card=cancelled reason="cancelled by the machine's stop; never finished"`, records)
		assert.NotContains(t, underStop, "card=set_aside")
		assert.NotContains(t, underStop, "finish=", "a cancelled card is never finished (EveryLaneReturnsOnStop)")
		assert.NotContains(t, underStop, "run is gone")
		assert.ElementsMatch(t, []string{
			"stop-return --as friend.bob c1@1 --epoch 15 --reason owned process stopped",
			"stop-return --as friend.bob c2@1 --epoch 15 --reason owned process stopped",
		}, r.sent(), "one stop-return each, after the run ended")
		assert.Regexp(t, `stop-return OK lane=\d card=c1@1 epoch=15 pid=0 exit=-1: handed back to friend.bob`, records)
		require.Len(t, state.StopReturns, 2, "the acks are recorded in the lane state")
		for _, sr := range state.StopReturns {
			assert.False(t, sr.Owed(), "%s: taken", sr.Card)
			assert.True(t, sr.Ended)
			assert.Equal(t, -1, sr.Exit)
		}
		assert.Empty(t, state.Started, "a cancelled card is not a started one")
		assert.Contains(t, records, "machine RUNNING: the lanes take cards again")
		for _, id := range []string{"c1", "c2"} {
			assert.Equal(t, 2, h.calls[id], "%s runs again once the machine runs (the queue still names it)", id)
			assert.FileExists(t, filepath.Join(dir, "outbox", id+"~15", "RESULT.md"))
		}
	})
}

// A refused stop-return stays owed, is tried again after StopReturnRetry (never a verb a
// second), its refusal said once while its text stands, and is counted on the beat; the
// server taking it ends the debt.
func TestARefusedStopReturnStaysOwedAndIsTriedAgainAfterTheRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		h := &stopWordHarness{lanesHarness: lh, calls: map[string]int{}, ended: map[string]bool{}}
		r, state := newStopRig(t, lh, 1)
		r.d.Deliver = h
		r.mu.Lock()
		r.refuse = assert.AnError
		r.mu.Unlock()
		var owedWhileRefused, sentWhileRefused int
		r.at[12] = func() { r.set(true) }
		r.at[50] = func() {
			owedWhileRefused, sentWhileRefused = r.d.OwedStopReturns(), len(r.sent())
			r.mu.Lock()
			r.refuse = nil
			r.mu.Unlock()
		}
		r.run(t, 140) // the steps are BeatEvery (1 s) apart: 140 s, two retries at most
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, owedWhileRefused, "owed while refused: the beat says so")
		assert.Equal(t, 1, sentWhileRefused, "one verb in the 38 s after the refusal, not one a second")
		assert.Equal(t, 0, r.d.OwedStopReturns(), "taken: nothing owed")
		assert.LessOrEqual(t, len(r.sent()), 3, "a retry a minute: %v", r.sent())
		assert.Equal(t, 1, strings.Count(records, "stop-return refused lane="), "the refusal said once while its text stands: %s", records)
		assert.Regexp(t, `stop-return refused lane=\d card=c1@1 epoch=15 pid=0 exit=-1 try=1: .*; tried again every 1m0s while it stands`, records)
		assert.Regexp(t, `stop-return OK lane=\d card=c1@1`, records)
		require.Len(t, state.StopReturns, 1)
		assert.False(t, state.StopReturns[0].Owed())
		assert.GreaterOrEqual(t, state.StopReturns[0].Tries, 1)
		assert.Empty(t, state.StopReturns[0].Refusal, "the refusal is cleared by the OK")
	})
}

// armOnOpenHarness arms the test's machine word when a lane's session opens: from the next
// step the word is read once by stopStep, once before the take and once before the start,
// so a word that turns on the third read turns between the claim and the start.
type armOnOpenHarness struct {
	*lanesHarness
	arm func()
}

func (h *armOnOpenHarness) OpenSession(ctx context.Context, seed string) (string, error) {
	id, err := h.lanesHarness.OpenSession(ctx, seed)
	h.arm()
	return id, err
}

// NoLaunchAfterStop at the take: the word turning between a lane's claim of a card and its
// start (the two reads of MachineStopped in one step) gives the card up at once: owed a
// stop-return, out of Started, its lane mark removed, so it is never a card held with no turn
// and never a run gone after a restart.
func TestAWordTurningBetweenTheClaimAndTheStartGivesTheCardUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		var mu sync.Mutex
		reads, armed := 0, false
		h := &armOnOpenHarness{lanesHarness: lh, arm: func() { mu.Lock(); armed = true; mu.Unlock() }}
		r, state := newStopRig(t, lh, 1)
		r.d.Deliver = h
		r.d.MachineStopped = func() bool {
			mu.Lock()
			defer mu.Unlock()
			if !armed {
				return false
			}
			reads++
			return reads >= 3 // stopStep, the read before the take, then the read before the start
		}
		r.run(t, 30)
		records := strings.Join(r.records, "\n")
		turns, _, _ := lh.got()
		assert.Empty(t, turns, "no turn began: the word turned before the start: %s", records)
		assert.Regexp(t, `lane 1: card c1@1 taken and not launched when the machine stopped: given up, stop-return owed`, records)
		assert.Equal(t, []string{"stop-return --as friend.bob c1@1 --epoch 15 --reason owned process stopped"}, r.sent())
		assert.Empty(t, state.Started, "not a started card")
		assert.NotContains(t, records, "run is gone")
		_, err := os.Stat(laneMarkPath(dir, "c1~15"))
		assert.True(t, os.IsNotExist(err), "the lane mark is removed: a lane may run it again once the machine runs")
	})
}

// The load replay forgets only owed records: a taken stop-return is the past, and the card's
// later run under the same job name that a restart finds begun is a run gone, FAILed as any.
func TestATakenStopReturnDoesNotHideALaterRunGone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		r, state := newStopRig(t, lh, 1)
		r.set(true)
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		*state = LaneState{
			Sessions:    map[int]string{1: "ses_old"},
			Started:     map[string]Started{"c1~15": {Lane: 1, Card: card, At: t0.Add(-time.Hour)}},
			StopReturns: []StopReturn{{Lane: 1, Job: "c1~15", Row: "friend.bob", Card: "c1", Gen: 1, Epoch: "15", Exit: -1, Ended: true, At: t0.Add(-2 * time.Hour), Result: "ok earlier"}},
		}
		r.run(t, 10)
		records := strings.Join(r.records, "\n")
		assert.Empty(t, r.sent(), "the taken record is not sent again")
		assert.Contains(t, records, "its run is gone", "the later run begun under the same job name is a run gone")
		assert.FileExists(t, filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
	})
}

// A read begun is listed by the queue's refresh as begun, not asked, so the asked list no
// longer names its generation: the stop still hands it back with the generation and epoch it
// was begun at.
func TestStopAfterAQueueRefreshStillHandsBackTheReadWithItsGen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, rblock: make(chan struct{})}
		sp := &readSprint{queue: `{"epoch":"15","cards":[{"id":"a.w1","col":"asked","gen":3,"packet":{"tier":"pro","head":"aaa","work_branch":"sprint/a","attempt":1,"brief":"REPO: o/r\nBASE: main\n","report":"Verdict: LAND"}}]}`}
		r := readRig(t, h, sp, 1)
		var mu sync.Mutex
		stopped := false
		r.d.MachineStopped = func() bool { mu.Lock(); defer mu.Unlock(); return stopped }
		r.d.StopReturn = func(_ context.Context, argv []string) error { _, err := sp.ask(context.Background(), argv); return err }
		// the read is begun by step 3; the queue is asked again every ReadAskEvery and lists it
		// begun; the word turns well after that
		r.at[int(ReadAskEvery/BeatEvery)+6] = func() { mu.Lock(); stopped = true; mu.Unlock() }
		r.run(t, int(ReadAskEvery/BeatEvery)+20)
		records := strings.Join(r.records, "\n")
		begun := sp.verbs("--begin")
		require.Len(t, begun, 1, "the read was begun once")
		var returns [][]string
		sp.mu.Lock()
		for _, a := range sp.argvs {
			if a[0] == "stop-return" {
				returns = append(returns, a)
			}
		}
		sp.mu.Unlock()
		require.Len(t, returns, 1, "one stop-return for the read: %s", records)
		assert.Equal(t, []string{"stop-return", "--as", "reader-bob", "a.w1@3", "--epoch", "15", "--reason", "owned process stopped"}, returns[0])
		assert.Regexp(t, `read a.w1@3 cancelled by stop`, records)
		assert.Empty(t, sp.verbs("--ok"))
		assert.Empty(t, sp.verbs("--return"), "no return verdict for a read the stop took")
	})
}

// A read card whose gen is unknown waits for its gen, never sends card@0.
func TestUnpopulatedReadCardWithUnknownGenWaitsForGenAndNeverEmitsCardAtZero(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, rblock: make(chan struct{})}
		sp := &readSprint{queue: `{"epoch":"15","cards":[]}`}
		r, state := laneRig(t, h.lanesHarness, 1)
		r.d.Deliver = h
		r.d.Sprint = sp.ask
		r.d.ReadSlots = func() int { return 1 }
		r.d.ReadModel = func(tier string) string { return map[string]string{"pro": "m-pro", "flash": "m-flash"}[tier] }
		var mu sync.Mutex
		var returns [][]string
		stopped := true
		r.d.MachineStopped = func() bool { mu.Lock(); defer mu.Unlock(); return stopped }
		r.d.StopReturn = func(_ context.Context, argv []string) error {
			mu.Lock()
			defer mu.Unlock()
			returns = append(returns, argv)
			return nil
		}
		*state = LaneState{
			Sessions: map[int]string{1: "s1"},
			StopReturns: []StopReturn{{
				Job: "read:r1", Row: "reader-bob", Card: "r1", Gen: 0, Epoch: "15", Exit: -1, Ended: true, At: t0,
			}},
		}
		r.run(t, 5)
		mu.Lock()
		assert.Empty(t, returns, "a read card with unknown gen never emits stop-return card@0")
		mu.Unlock()

		// Now reader queue returns r1 with Gen 2
		sp.queue = `{"epoch":"15","cards":[{"id":"r1","col":"asked","gen":2,"packet":{"tier":"pro","head":"aaa","work_branch":"sprint/a","attempt":1,"brief":"REPO: o/r\nBASE: main\n","report":"Verdict: LAND"}}]}`
		r.run(t, int(ReadAskEvery/BeatEvery)+5)
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, returns, 1, "once gen is known, stop-return is sent")
		assert.Equal(t, []string{"stop-return", "--as", "reader-bob", "r1@2", "--epoch", "15", "--reason", "owned process stopped"}, returns[0])
	})
}

// StopReturnsSurviveRestart: a daemon starting up with a stop-return owed in its lane state
// (the stop cancelled the lane, the daemon died before the ack) sends it first, and never
// finishes the card as a run gone.
func TestAStopReturnOwedSurvivesTheDaemonsRestartAndIsSentFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "working"}}, []string{"c1"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		r, state := newStopRig(t, lh, 1)
		r.set(true)
		card := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
		*state = LaneState{
			Sessions:    map[int]string{1: "ses_old"},
			Started:     map[string]Started{"c1~15": {Lane: 1, Card: card, At: t0.Add(-time.Hour)}},
			StopReturns: []StopReturn{{Lane: 1, Job: "c1~15", Row: "friend.bob", Card: "c1", Gen: 1, Epoch: "15", Exit: -1, At: t0.Add(-time.Minute)}},
		}
		r.run(t, 10)
		records := strings.Join(r.records, "\n")
		assert.Equal(t, []string{"stop-return --as friend.bob c1@1 --epoch 15 --reason owned process stopped"}, r.sent())
		assert.NotContains(t, records, "run is gone", "the stop's card is owed a return, never a FAIL")
		assert.NoFileExists(t, filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
		assert.Empty(t, state.Started)
		require.Len(t, state.StopReturns, 1)
		assert.False(t, state.StopReturns[0].Owed())
		assert.True(t, state.StopReturns[0].Ended)
		turns, _, _ := lh.got()
		assert.Empty(t, turns, "no turn while STOPPED")
	})
}

// NoNudgeWhileStopped: an idle session holding cards gets no wake turn and the coordinator no
// idle note while the machine is STOPPED; the ping and the messages still flow (their own
// tests), and the stop lifting brings the wake back on its own clock.
func TestNoWakeOrIdleNoteWhileStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newIdleRig(t, "c-old", "c-new")
		var mu sync.Mutex
		stopped := true
		r.d.MachineStopped = func() bool { mu.Lock(); defer mu.Unlock(); return stopped }
		r.at[30] = func() {
			assert.Empty(t, r.wakes(), "no wake turn while STOPPED, however idle")
			assert.Empty(t, r.notes(t), "no idle note while STOPPED")
			mu.Lock()
			stopped = false
			mu.Unlock()
		}
		r.run(t, 45)
		assert.Len(t, r.wakes(), 1, "the wake comes once the machine runs again")
	})
}

// A lane state file written by this build reads back with its stop-returns.
func TestLaneStateKeepsItsStopReturns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir
	want := LaneState{Sessions: map[int]string{1: "s"}, StopReturns: []StopReturn{{Lane: 1, Job: "c1~15", Row: "friend.bob", Card: "c1", Gen: 2, Epoch: "15", Pid: 41, Exit: -1, Ended: true, At: t0, Result: "ok"}}}
	require.NoError(t, WriteLanes(path, want))
	got, err := ReadLanes(path)
	require.NoError(t, err)
	assert.Equal(t, want.StopReturns, got.StopReturns)
	_, err = os.Stat(filepath.Join(dir, LanesFile))
	require.NoError(t, err)
}

// Regression test for Fix 3: stopReads falls back to readSet.epoch and asked queue epoch
// when r.Epoch is unpopulated on an asked/active read.
func TestStopReadsFallsBackToReadSetEpochWhenReadEpochUnpopulated(t *testing.T) {
	t.Parallel()
	d := &Daemon{Friend: "bob", Record: func(string) {}}
	cancelled := false
	cancel := func() { cancelled = true }
	rs := &readSet{
		cancel:  map[string]context.CancelFunc{"r1": cancel},
		stopped: map[string]bool{},
		active:  map[string]AskedRead{"r1": {ID: "r1", Gen: 2, Epoch: ""}}, // r.Epoch unpopulated!
		epoch:   "42",                                                      // fallback epoch on readSet
	}
	ls := &laneSet{state: LaneState{}}
	l := &loop{d: d, reads: rs, lanes: ls}
	now := time.Now()
	l.stopReads(now)

	require.True(t, cancelled, "read was cancelled")
	require.True(t, rs.stopped["r1"], "marked stopped")
	require.Len(t, ls.state.StopReturns, 1)
	sr := ls.state.StopReturns[0]
	assert.Equal(t, "42", sr.Epoch, "falls back to readSet.epoch when r.Epoch is unpopulated")
	assert.Equal(t, "r1", sr.Card)
	assert.Equal(t, 2, sr.Gen)
	assert.Equal(t, "reader-bob", sr.Row)

	// Also test fallback to parsed asked epoch when both r.Epoch and s.epoch are unpopulated
	cancelled2 := false
	rs2 := &readSet{
		cancel:  map[string]context.CancelFunc{"r2": func() { cancelled2 = true }},
		stopped: map[string]bool{},
		active:  map[string]AskedRead{"r2": {ID: "r2", Gen: 3, Epoch: ""}},
		asked:   []AskedRead{{ID: "r3", Gen: 1, Epoch: "99"}},
	}
	ls2 := &laneSet{state: LaneState{}}
	l2 := &loop{d: d, reads: rs2, lanes: ls2}
	l2.stopReads(now)
	require.True(t, cancelled2, "read was cancelled")
	require.Len(t, ls2.state.StopReturns, 1)
	assert.Equal(t, "99", ls2.state.StopReturns[0].Epoch, "falls back to asked epoch")
}

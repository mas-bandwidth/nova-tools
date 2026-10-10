package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const deferredBrief = "inbox/card.w1~15/BRIEF.md (card card.w1, ready on her row)"
const deferredBriefStarted = "inbox/card.w2~15/BRIEF.md (card card.w2, ready on her row)"

type captureDeliver struct {
	mu    sync.Mutex
	texts []string
}

func (c *captureDeliver) Deliver(_ context.Context, text string) (int, error) {
	c.mu.Lock()
	c.texts = append(c.texts, text)
	c.mu.Unlock()
	return 0, nil
}

type wordBox struct {
	pause, stop bool
	known       bool
}

func (w *wordBox) daemon(t *testing.T, records *[]string, deliver Deliverer) *Daemon {
	t.Helper()
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	d := &Daemon{
		Friend:         "bob",
		Dir:            t.TempDir(),
		Deliver:        deliver,
		Record:         func(line string) { *records = append(*records, line) },
		MachinePaused:  func() bool { return w.pause },
		MachineStopped: func() bool { return w.stop },
		MachineKnown:   func() bool { return w.known },
	}
	d.m = Start(now)
	return d
}

func nudgeLoop(d *Daemon) *loop {
	return &loop{
		d:       d,
		ctx:     context.Background(),
		results: make(chan result, 4),
		mode:    ModeBatch,
		acted:   map[string]bool{},
		inHand:  map[string]bool{},
		failed:  map[string]int{},
		lanes:   &laneSet{loaded: true, state: LaneState{Started: map[string]Started{}}},
	}
}

func deferredEvents(records []string) []string {
	var out []string
	for _, line := range records {
		if !strings.Contains(line, "deferred-start ") {
			continue
		}
		if strings.Contains(line, "BRIEF.md") || strings.Contains(line, "event=native_start") {
			panic("deferred-start marker carried a brief or claimed native_start: " + line)
		}
		i := strings.Index(line, "deferred-start ")
		out = append(out, line[i:])
	}
	return out
}

func TestDeferredDealtNudgeIsHeldOnPauseAndResumesWithoutNativeStart(t *testing.T) {
	t.Parallel()
	var records []string
	deliver := &captureDeliver{}
	w := &wordBox{known: true}
	d := w.daemon(t, &records, deliver)
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)

	require.True(t, l.startOwedDealt(now))
	got := <-l.results
	require.Contains(t, got.t.text, "Read each BRIEF.md and start")
	require.Contains(t, got.t.text, deferredBrief)
	_, err := os.Stat(filepath.Join(d.Dir, NudgeDebtFile))
	require.NoError(t, err, "the owed nudge is a file, not only memory")
	l.batchDone(result{t: got.t, err: Deferred{Reason: "the chat is open in the app"}}, now.Add(time.Second))

	w.pause = true
	l.parkWorkNudges(now.Add(2 * time.Second))
	assert.Nil(t, l.busy)
	assert.Equal(t, []string{deferredBrief}, l.owedBriefs)
	assert.Equal(t, 1, l.owedAttempt)
	assert.False(t, l.startOwedDealt(now.Add(3*time.Second)), "no start while paused")
	deliver.mu.Lock()
	assert.Len(t, deliver.texts, 1, "the deferred attempt is not sent again while paused")
	deliver.mu.Unlock()

	w.pause = false
	require.True(t, l.startOwedDealt(now.Add(4*time.Second)))
	resumed := <-l.results
	assert.Equal(t, 1, resumed.t.attempt, "pause keeps the attempt id")
	assert.Contains(t, resumed.t.text, deferredBrief)
	l.batchDone(result{t: resumed.t, exit: 0}, now.Add(5*time.Second))
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, led.Records)
	assert.Equal(t, nudgeAccepted, led.Records[0].Phase, "accepted is a tombstone, not a deleted file")

	assert.Equal(t, []string{
		"deferred-start attempt=1 event=brief_staged auth=RUNNING",
		"deferred-start attempt=1 event=work_delivery_begin auth=RUNNING",
		"deferred-start attempt=1 event=delivery_deferred auth=RUNNING",
		"deferred-start attempt=1 event=work_retry_held auth=PAUSED",
		"deferred-start attempt=1 event=work_delivery_begin auth=RUNNING",
		"deferred-start attempt=1 event=delivery_accepted auth=RUNNING",
	}, deferredEvents(records))
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestParkedNudgeDebtSurvivesANewLoopAndSkipsStartedWork(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{pause: true, known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.busy = &turn{
		nudge: true, attempt: 7, subjects: `"2 card(s) dealt"`,
		dealt: []string{deferredBrief, deferredBriefStarted},
	}
	l.retry = now.Add(-time.Second)
	require.True(t, l.retryDeferred(now))
	_, err := os.Stat(filepath.Join(d.Dir, NudgeDebtFile))
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "jobs", "card.w2~15"), 0o755))
	staged := nudgeLoop(d)
	staged.recoverNudgeDebt(now.Add(time.Second))
	assert.Equal(t, []string{deferredBrief, deferredBriefStarted}, staged.owedBriefs, "jobs/ exists after Stage and is not a start")
	assert.Equal(t, 7, staged.owedAttempt)

	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "outbox", "card.w2~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "outbox", "card.w2~15", "REPORT.md"), []byte("done\n"), 0o644))
	finished := nudgeLoop(d)
	finished.recoverNudgeDebt(now.Add(2 * time.Second))
	assert.Equal(t, []string{deferredBrief}, finished.owedBriefs)
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestInflightNudgeIsHeldNotReplayed(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, NudgeDebtFile), []byte("{\n  \"nextAttempt\": 2,\n  \"records\": [\n    {\"attempt\": 1, \"phase\": \"inflight\", \"briefs\": [\""+deferredBrief+"\"]}\n  ]\n}\n"), 0o644))
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.recoverNudgeDebt(now)
	assert.Empty(t, l.owedBriefs)
	assert.False(t, l.startOwedDealt(now))
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestBadNudgeLedgerBlocksWithoutRewrite(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	raw := []byte("{")
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, NudgeDebtFile), raw, 0o644))
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.recoverNudgeDebt(now)
	assert.True(t, l.nudgeDebtBlocked)
	assert.False(t, l.startOwedDealt(now))
	got, err := os.ReadFile(filepath.Join(d.Dir, NudgeDebtFile))
	require.NoError(t, err)
	assert.Equal(t, raw, got)
}

func TestUndurableNudgeParkKeepsTheTurn(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{pause: true, known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	file := filepath.Join(d.Dir, "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	d.Dir = file
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.busy = &turn{nudge: true, attempt: 1, subjects: `"1 card(s) dealt"`, dealt: []string{deferredBrief}}
	l.parkWorkNudges(now)
	assert.NotNil(t, l.busy, "a failed ledger write does not clear the turn")
}

func TestParkedNudgeLeavesOrdinaryRequestPending(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{pause: true, known: true}
	l := nudgeLoop(w.daemon(t, &records, &captureDeliver{}))
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.hand = []bus.Entry{{Stream: "johnny", Entry: "req-1"}}
	l.inHand = map[string]bool{"req-1": true}
	l.busy = &turn{
		nudge: true, attempt: 1, subjects: `"1 card(s) dealt"`,
		dealt: []string{deferredBrief},
	}
	l.parkWorkNudges(now)
	assert.Equal(t, []bus.Entry{{Stream: "johnny", Entry: "req-1"}}, l.hand)
	assert.True(t, l.inHand["req-1"], "park does not ack the request")
	assert.Nil(t, l.busy)
	assert.Equal(t, []string{deferredBrief}, l.owedBriefs)
}

func TestDeferredRequestRetriesWhilePaused(t *testing.T) {
	t.Parallel()
	var records []string
	deliver := &captureDeliver{}
	w := &wordBox{pause: true, known: true}
	l := nudgeLoop(w.daemon(t, &records, deliver))
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.busy = &turn{
		subjects: `"review"`,
		text:     "kind=request subject=review\n",
		msgs:     []bus.Message{{ID: "req-1", Subject: "review", Kind: bus.KindRequest}},
	}
	l.retry = now.Add(-time.Second)
	require.True(t, l.retryDeferred(now))
	got := <-l.results
	assert.Contains(t, got.t.text, "kind=request subject=review")
	assert.NotContains(t, strings.Join(records, "\n"), "work_retry_held")
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestRetryMarksInflightBeforeLaunchAndRestartHoldsIt(t *testing.T) {
	t.Parallel()
	var records []string
	deliver := &captureDeliver{}
	w := &wordBox{known: true}
	d := w.daemon(t, &records, deliver)
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	require.True(t, l.startOwedDealt(now))
	got := <-l.results
	l.batchDone(result{t: got.t, err: Deferred{Reason: "the chat is open in the app"}}, now.Add(time.Second))

	l.retry = now.Add(2 * time.Second)
	require.True(t, l.retryDeferred(now.Add(2*time.Second)))
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, led.Records)
	assert.Equal(t, nudgeInflight, led.Records[0].Phase, "a retry is uncertain before the session answers")
	<-l.results

	held := nudgeLoop(d)
	held.recoverNudgeDebt(now.Add(3 * time.Second))
	assert.Empty(t, held.owedBriefs, "inflight is not replayed")
	assert.False(t, held.startOwedDealt(now.Add(3*time.Second)))
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestUndurableRetryDoesNotLaunch(t *testing.T) {
	t.Parallel()
	var records []string
	deliver := &captureDeliver{}
	w := &wordBox{known: true}
	d := w.daemon(t, &records, deliver)
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	require.True(t, l.startOwedDealt(now))
	got := <-l.results
	l.batchDone(result{t: got.t, err: Deferred{Reason: "the chat is open in the app"}}, now.Add(time.Second))
	file := filepath.Join(d.Dir, "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	d.Dir = file
	l.retry = now.Add(2 * time.Second)
	require.True(t, l.retryDeferred(now.Add(2*time.Second)))
	deliver.mu.Lock()
	assert.Len(t, deliver.texts, 1, "a failed inflight write does not send the retry")
	deliver.mu.Unlock()
	assert.NotNil(t, l.busy)
}

func TestAcceptedWriteFailureLeavesInflight(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	require.True(t, l.startOwedDealt(now))
	got := <-l.results
	orig := d.Dir
	file := filepath.Join(orig, "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	d.Dir = file
	l.batchDone(result{t: got.t, exit: 0}, now.Add(time.Second))
	assert.Nil(t, l.busy, "the session already accepted; the turn is not sent again in-process")
	d.Dir = orig
	var led nudgeLedger
	found, err := read(filepath.Join(orig, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, led.Records)
	assert.Equal(t, nudgeInflight, led.Records[0].Phase, "a failed tombstone leaves uncertain, which is not replayed")
	restarted := nudgeLoop(d)
	restarted.recoverNudgeDebt(now.Add(2 * time.Second))
	assert.Empty(t, restarted.owedBriefs)
	assert.False(t, restarted.startOwedDealt(now.Add(2*time.Second)))
	assert.Contains(t, strings.Join(records, "\n"), "event=delivery_accepted")
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestResultAndLaneClaimAreFinishedProof(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	raw := []byte("{\n  \"nextAttempt\": 2,\n  \"records\": [\n    {\"attempt\": 1, \"phase\": \"pending\", \"briefs\": [\"" + deferredBrief + "\"]}\n  ]\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, NudgeDebtFile), raw, 0o644))
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)

	claimed := nudgeLoop(d)
	claimed.lanes.state.Started["card.w1~15"] = Started{}
	claimed.recoverNudgeDebt(now)
	assert.Empty(t, claimed.owedBriefs, "a lane claim is finished proof")

	onLane := nudgeLoop(d)
	onLane.lanes.lanes = []*lane{{card: &Card{Outbox: filepath.Join(d.Dir, "outbox", "card.w1~15")}}}
	onLane.recoverNudgeDebt(now)
	assert.Empty(t, onLane.owedBriefs, "a lane card is finished proof")

	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "outbox", "card.w1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "outbox", "card.w1~15", "RESULT.md"), []byte("done\n"), 0o644))
	finished := nudgeLoop(d)
	finished.recoverNudgeDebt(now)
	assert.Empty(t, finished.owedBriefs, "RESULT.md is finished proof")
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestStartOwedDealtStaysBehindBatchLaneDebt(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	l := nudgeLoop(w.daemon(t, &records, &captureDeliver{}))
	l.lanes.loaded = false
	l.owedBriefs = []string{deferredBrief}
	l.owedAttempt = 2
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	assert.False(t, l.startOwedDealt(now), "unsettled batch lane debt holds the nudge")
	assert.Equal(t, []string{deferredBrief}, l.owedBriefs)
}

func TestIdleWakeRetriesWithoutALedger(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.busy = &turn{nudge: true, subjects: `"idle wake"`, text: "nova-friend: you hold 1 cards\n"}
	l.retry = now.Add(-time.Second)
	require.True(t, l.retryDeferred(now))
	got := <-l.results
	assert.Contains(t, got.t.text, "you hold 1 cards")
	_, err := os.Stat(filepath.Join(d.Dir, NudgeDebtFile))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestPausedIdleWakeDoesNotStick(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{pause: true, known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.busy = &turn{nudge: true, subjects: `"idle wake"`, text: "nova-friend: you hold 1 cards\n"}
	l.retry = now.Add(-time.Second)
	require.True(t, l.retryDeferred(now))
	assert.Nil(t, l.busy, "an idle wake is not brief debt and does not hold the slot")
	_, err := os.Stat(filepath.Join(d.Dir, NudgeDebtFile))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestStartOwedDealtKeepsUnpersistedLines(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	raw := []byte("{\n  \"nextAttempt\": 2,\n  \"records\": [\n    {\"attempt\": 1, \"phase\": \"pending\", \"briefs\": [\"" + deferredBrief + "\"]}\n  ]\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, NudgeDebtFile), raw, 0o644))
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{"not a brief"}
	assert.False(t, l.startOwedDealt(now))
	assert.Equal(t, []string{"not a brief"}, l.dealt)
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, led.Records)
	assert.Equal(t, nudgePending, led.Records[0].Phase)
}

func TestUnparsableDealtLineIsKept(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief, "not a brief"}
	l.persistStagedBriefs(now)
	assert.Empty(t, l.dealt, "a durable refusal is not only memory")
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, led.Records, 2)
	assert.Equal(t, nudgePending, led.Records[0].Phase)
	assert.Equal(t, []string{deferredBrief}, led.Records[0].Briefs)
	assert.Equal(t, nudgeRefused, led.Records[1].Phase)
	assert.Equal(t, []string{"not a brief"}, led.Records[1].Briefs)
	assert.Contains(t, strings.Join(records, "\n"), "unparsable brief")
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
	restarted := nudgeLoop(d)
	restarted.recoverNudgeDebt(now)
	assert.Equal(t, []string{deferredBrief}, restarted.owedBriefs, "a refused line is not replayed")
}

func TestInflightTurnMustStay(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	l := nudgeLoop(w.daemon(t, &records, &captureDeliver{}))
	held := &turn{nudge: true, attempt: 1, dealt: []string{deferredBrief}}
	l.nudgeLedger = nudgeLedger{NextAttempt: 2, Records: []nudgeRecord{{Attempt: 1, Phase: nudgeInflight, Briefs: []string{deferredBrief}}}}
	assert.True(t, l.nudgeTurnMustStay(held))
	l.nudgeLedger.Records[0].Phase = nudgeDeferred
	assert.False(t, l.nudgeTurnMustStay(held))
	assert.False(t, l.nudgeTurnMustStay(&turn{nudge: true, subjects: `"idle wake"`}))
}

func TestOneShotStageKeepsTheLineUntilBatch(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	l.mode = ModeOneShot
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "jobs", "card.w1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "jobs", "card.w1~15", "JOB.md"), []byte("job\n"), 0o644))
	d.stageDealt = map[string]string{"card.w1~15": deferredBrief}
	l.flushStageDealt(now)
	assert.Empty(t, d.stageDealt, "the line is forgotten only after the ledger has it")
	assert.Empty(t, l.dealt, "one-shot does not take the batch nudge")
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, led.Records)
	assert.Equal(t, nudgePending, led.Records[0].Phase)
	l.mode = ModeBatch
	require.True(t, l.startOwedDealt(now.Add(time.Second)))
	got := <-l.results
	assert.Contains(t, got.t.text, deferredBrief)
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestRestartRebuildsAStagedJobAndSkipsAnUnstagedOne(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "inbox", "card.w1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "inbox", "card.w1~15", "BRIEF.md"), []byte("brief\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "jobs", "card.w1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "jobs", "card.w1~15", "JOB.md"), []byte("job\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "inbox", "card.w2~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "inbox", "card.w2~15", "BRIEF.md"), []byte("brief\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "jobs", "card.w2~15"), 0o755))

	rebuilt := nudgeLoop(d)
	rebuilt.recoverNudgeDebt(now)
	assert.Equal(t, []string{"inbox/card.w1~15/BRIEF.md"}, rebuilt.owedBriefs, "JOB.md plus the brief is the crash evidence")
	require.True(t, rebuilt.startOwedDealt(now))
	got := <-rebuilt.results
	assert.Contains(t, got.t.text, "inbox/card.w1~15/BRIEF.md")
	assert.NotContains(t, got.t.text, "card.w2~15", "a job directory without JOB.md is not nudged")
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	for _, r := range led.Records {
		for _, line := range r.Briefs {
			assert.NotContains(t, line, "card.w2~15")
		}
	}
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestStageStepDoesNotDropTheLine(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	l.mode = ModeOneShot
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	d.stageDealt = map[string]string{"card.w1~15": deferredBrief}
	d.staging = map[string]bool{"card.w1~15": true}
	d.stageDone = []stageResult{{
		p:   Packet{Job: "card.w1~15", Card: "card.w1", Repo: "mas-bandwidth/nova", Base: "main", Branch: "work"},
		sha: "0123456789abcdef0123456789abcdef01234567",
	}}
	l.stageStep(nil, now)
	assert.Equal(t, deferredBrief, d.stageDealt["card.w1~15"], "stage success no longer deletes the line")
	_, err := os.Stat(filepath.Join(d.Dir, NudgeDebtFile))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestOneShotRestartKeepsAnExistingDurableRow(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	raw := []byte("{\n  \"nextAttempt\": 2,\n  \"records\": [\n    {\"attempt\": 1, \"phase\": \"accepted\", \"briefs\": [\"" + deferredBrief + "\"]}\n  ]\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, NudgeDebtFile), raw, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "jobs", "card.w1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "jobs", "card.w1~15", "JOB.md"), []byte("job\n"), 0o644))
	l := nudgeLoop(d)
	l.mode = ModeOneShot
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	d.stageDealt = map[string]string{"card.w1~15": deferredBrief}
	l.flushStageDealt(now)
	assert.Empty(t, d.stageDealt, "the staged line is already in the ledger")
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, led.Records, 1, "a one-shot restart must not append a second pending row")
	assert.Equal(t, 1, led.Records[0].Attempt)
	assert.Equal(t, nudgeAccepted, led.Records[0].Phase)
	assert.Equal(t, 2, led.NextAttempt)
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestAnnotatedLineDoesNotDuplicateAReconstructedJob(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	shortLine := "inbox/card.w1~15/BRIEF.md"
	raw := []byte("{\n  \"nextAttempt\": 2,\n  \"records\": [\n    {\"attempt\": 1, \"phase\": \"pending\", \"briefs\": [\"" + shortLine + "\"]}\n  ]\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, NudgeDebtFile), raw, 0o644))
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	assert.Empty(t, l.dealt)
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, led.Records, 1, "the annotated line is the same job as the bare brief path")
	assert.Equal(t, shortLine, led.Records[0].Briefs[0])
	assert.Equal(t, nudgePending, led.Records[0].Phase)
}

func TestFinishedBriefIsNotWrittenPending(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "outbox", "card.w1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "outbox", "card.w1~15", "REPORT.md"), []byte("done\n"), 0o644))
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	assert.Empty(t, l.dealt)
	_, err := os.Stat(filepath.Join(d.Dir, NudgeDebtFile))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func presentTurn(queue []QueueLine, now time.Time) *turn {
	return &turn{
		present:  true,
		subjects: `"present"`,
		text:     PresentText("bob", "ada", now, queue, Skipped{}, nil, "", ""),
		covered:  jobsInQueue(queue),
	}
}

func phasesByJob(t *testing.T, dir string) map[string]string {
	t.Helper()
	var led nudgeLedger
	found, err := read(filepath.Join(dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	out := map[string]string{}
	for _, r := range led.Records {
		for _, line := range r.Briefs {
			job, ok := jobFromBriefLine(line)
			require.True(t, ok, line)
			out[job] = r.Phase
		}
	}
	return out
}

func TestPresentSupersedesAnUnsentPendingNudge(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	queue := []QueueLine{{Card: "card.w1", Col: "working", Brief: "inbox/card.w1~15/BRIEF.md"}}
	turn := presentTurn(queue, now)
	assert.Contains(t, turn.text, "Your live queue, 1 card(s) on your row:\n- card.w1 working inbox/card.w1~15/BRIEF.md\n")
	l.batchDone(result{t: turn, exit: 0}, now)
	assert.False(t, l.startOwedDealt(now.Add(time.Second)), "a present that named the job replaces that nudge")
	assert.Equal(t, nudgeSuperseded, phasesByJob(t, d.Dir)["card.w1~15"])
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestPresentReadFailureKeepsPending(t *testing.T) {
	t.Parallel()
	var records []string
	deliver := &captureDeliver{}
	w := &wordBox{known: true}
	d := w.daemon(t, &records, deliver)
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	store := bustest.NewFake(now, "bob")
	store.Fail = errors.New("stream down")
	d.Store = store
	l.startPresent(now, true)
	assert.Equal(t, nudgePending, phasesByJob(t, d.Dir)["card.w1~15"], "a present that does not dispatch must not retire the nudge")
	assert.Contains(t, strings.Join(records, "\n"), "present: not yet")
	require.True(t, l.startOwedDealt(now.Add(time.Second)), "the nudge is still owed")
	got := <-l.results
	assert.Contains(t, got.t.text, "Read each BRIEF.md and start")
	assert.Contains(t, got.t.text, deferredBrief)
	assert.Equal(t, nudgeInflight, phasesByJob(t, d.Dir)["card.w1~15"])
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
	deliver.mu.Lock()
	assert.NotEmpty(t, deliver.texts)
	deliver.mu.Unlock()
}

func TestDeferredPresentKeepsPending(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	queue := []QueueLine{{Card: "card.w1", Col: "working", Brief: "inbox/card.w1~15/BRIEF.md"}}
	turn := presentTurn(queue, now)
	assert.Contains(t, turn.text, "inbox/card.w1~15/BRIEF.md")
	// startTurn holds the present as the busy turn before the session answers.
	// A deferral returns without clearing it, so the owed nudge does not launch.
	l.busy = turn
	l.batchDone(result{t: turn, err: Deferred{Reason: "the chat is open in the app"}}, now)
	assert.Equal(t, turn, l.busy, "a deferred present stays the busy turn")
	assert.Equal(t, nudgePending, phasesByJob(t, d.Dir)["card.w1~15"])
	assert.False(t, l.startOwedDealt(now.Add(time.Second)), "a deferred present still holds the turn")
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestPresentCrashBeforeAcceptKeepsPending(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	restarted := nudgeLoop(d)
	restarted.recoverNudgeDebt(now.Add(time.Second))
	assert.Equal(t, []string{deferredBrief}, restarted.owedBriefs, "a crash before exit 0 leaves the nudge replayable")
	assert.Equal(t, nudgePending, phasesByJob(t, d.Dir)["card.w1~15"])
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestPresentSupersedesOnlyJobsItNames(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	omitted := "inbox/card.w2~15/BRIEF.md (card card.w2, ready on her row)"
	held := "inbox/card.w3~15/BRIEF.md (card card.w3, ready on her row)"
	l.dealt = []string{deferredBrief, omitted}
	l.persistStagedBriefs(now)
	require.NoError(t, l.appendNudgeRecord(nudgeInflight, []string{held}, now))
	queue := []QueueLine{{Card: "card.w1", Col: "working", Brief: "inbox/card.w1~15/BRIEF.md"}}
	turn := presentTurn(queue, now)
	assert.Contains(t, turn.text, "Your live queue, 1 card(s) on your row:\n- card.w1 working inbox/card.w1~15/BRIEF.md\n")
	assert.NotContains(t, turn.text, "card.w2")
	l.batchDone(result{t: turn, exit: 0}, now)
	phases := phasesByJob(t, d.Dir)
	assert.Equal(t, nudgeSuperseded, phases["card.w1~15"])
	assert.Equal(t, nudgePending, phases["card.w2~15"], "a job the present did not name stays pending")
	assert.Equal(t, nudgeInflight, phases["card.w3~15"], "an inflight row is not a pending nudge")
	require.True(t, l.startOwedDealt(now.Add(time.Second)))
	got := <-l.results
	assert.Contains(t, got.t.text, omitted)
	assert.NotContains(t, got.t.text, "card.w1~15")
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestAStaleBatchSessionStillOwesThePresentAfterTheBriefIsLedgered(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
	l.delivered = now.Add(-StaleAfter)
	assert.False(t, l.presentOwed(now), "an idle stale session with nothing waiting is not a present")
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	assert.Empty(t, l.dealt, "the line now lives in the ledger")
	assert.True(t, l.presentOwed(now), "the pending brief is still waiting, so the present comes first")
	l.delivered = now
	assert.False(t, l.presentOwed(now.Add(time.Second)), "inside the bound the same brief is not a present")
	_, err := l.markNudgePhase(1, nudgeInflight, nil, now)
	require.NoError(t, err)
	l.delivered = now.Add(-StaleAfter)
	l.owedBriefs = []string{deferredBrief}
	l.nudgeDebtLoaded = false
	assert.False(t, l.presentOwed(now), "an inflight row is not waiting, and owedBriefs is not the authority")
}

func TestASettledOrFinishedBriefDoesNotOweThePresent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
	for _, phase := range []string{nudgeAccepted, nudgeSuperseded} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			var records []string
			w := &wordBox{known: true}
			d := w.daemon(t, &records, &captureDeliver{})
			l := nudgeLoop(d)
			l.delivered = now.Add(-StaleAfter)
			l.dealt = []string{deferredBrief}
			l.persistStagedBriefs(now)
			_, err := l.markNudgePhase(1, phase, nil, now)
			require.NoError(t, err)
			l.owedBriefs = []string{deferredBrief}
			l.nudgeDebtLoaded = false
			assert.False(t, l.presentOwed(now), "a settled nudge does not stay a present")
		})
	}
	t.Run("finished", func(t *testing.T) {
		t.Parallel()
		var records []string
		w := &wordBox{known: true}
		d := w.daemon(t, &records, &captureDeliver{})
		l := nudgeLoop(d)
		l.delivered = now.Add(-StaleAfter)
		l.dealt = []string{deferredBrief}
		l.persistStagedBriefs(now)
		require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "outbox", "card.w1~15"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "outbox", "card.w1~15", "RESULT.md"), []byte("done\n"), 0o644))
		l.owedBriefs = []string{deferredBrief}
		assert.False(t, l.presentOwed(now), "a finished job is not a brief waiting")
	})
}

func TestAPresentThatDoesNotExitZeroKeepsPending(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	queue := []QueueLine{{Card: "card.w1", Col: "working", Brief: "inbox/card.w1~15/BRIEF.md"}}
	cases := []struct {
		name string
		done func(turn *turn) result
	}{
		{name: "nonzero", done: func(turn *turn) result { return result{t: turn, exit: 1} }},
		{name: "stopped", done: func(turn *turn) result {
			turn.stopped = true
			return result{t: turn, exit: 0}
		}},
		{name: "refused", done: func(turn *turn) result {
			return result{t: turn, err: SessionRefused{Session: "ses", Reason: "the session refused the turn"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var records []string
			w := &wordBox{known: true}
			d := w.daemon(t, &records, &captureDeliver{})
			l := nudgeLoop(d)
			l.dealt = []string{deferredBrief}
			l.persistStagedBriefs(now)
			turn := presentTurn(queue, now)
			l.batchDone(tc.done(turn), now)
			var led nudgeLedger
			found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
			require.NoError(t, err)
			require.True(t, found)
			require.Len(t, led.Records, 1)
			assert.Equal(t, 1, led.Records[0].Attempt, "the same attempt stays pending")
			assert.Equal(t, nudgePending, led.Records[0].Phase)
			assert.Equal(t, []string{deferredBrief}, led.Records[0].Briefs)
			assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
		})
	}
}

func TestRestartAfterAFailedCoverageSaveKeepsTheSameAttempt(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	require.NoError(t, os.Chmod(d.Dir, 0o555))
	defer os.Chmod(d.Dir, 0o755)
	queue := []QueueLine{{Card: "card.w1", Col: "working", Brief: "inbox/card.w1~15/BRIEF.md"}}
	l.batchDone(result{t: presentTurn(queue, now), exit: 0}, now)
	require.NoError(t, os.Chmod(d.Dir, 0o755))
	restarted := nudgeLoop(d)
	restarted.recoverNudgeDebt(now.Add(time.Second))
	assert.Equal(t, []string{deferredBrief}, restarted.owedBriefs)
	var led nudgeLedger
	found, err := read(filepath.Join(d.Dir, NudgeDebtFile), &led)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, led.Records, 1)
	assert.Equal(t, 1, led.Records[0].Attempt)
	assert.Equal(t, nudgePending, led.Records[0].Phase)
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestPresentCoverageWriteFailureKeepsPending(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	d := w.daemon(t, &records, &captureDeliver{})
	l := nudgeLoop(d)
	now := time.Date(2026, 10, 9, 0, 53, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	require.NoError(t, os.Chmod(d.Dir, 0o555))
	defer os.Chmod(d.Dir, 0o755)
	queue := []QueueLine{{Card: "card.w1", Col: "working", Brief: "inbox/card.w1~15/BRIEF.md"}}
	turn := presentTurn(queue, now)
	assert.Contains(t, turn.text, "inbox/card.w1~15/BRIEF.md")
	l.batchDone(result{t: turn, exit: 0}, now)
	assert.Equal(t, nudgePending, phasesByJob(t, d.Dir)["card.w1~15"])
	assert.Contains(t, strings.Join(records, "\n"), "present coverage not durable, pending kept")
	assert.False(t, l.startOwedDealt(now.Add(time.Second)), "a failed coverage write must not launch the nudge")
	require.NoError(t, os.Chmod(d.Dir, 0o755))
	require.NoError(t, l.retryPresentCoverage(now.Add(2*time.Second)))
	assert.Equal(t, nudgeSuperseded, phasesByJob(t, d.Dir)["card.w1~15"])
	assert.False(t, l.startOwedDealt(now.Add(3*time.Second)))
	assert.NotContains(t, strings.Join(records, "\n"), "event=native_start")
}

func TestRecoveredNudgeRequiresCurrentRowBeforeInitialOrRetryDelivery(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"unknown", "removed", "refresh-error", "missing-brief", "current"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			var records []string
			deliver := &captureDeliver{}
			w := &wordBox{known: true}
			d := w.daemon(t, &records, deliver)
			d.Held = func(context.Context) (Row, error) { return Row{Reads: true, From: FromCards}, nil }
			job := "card.w1~15"
			brief := filepath.Join(d.Dir, "inbox", job, "BRIEF.md")
			require.NoError(t, os.MkdirAll(filepath.Dir(brief), 0700))
			require.NoError(t, os.WriteFile(brief, []byte("STATUS: nova-sprint card card.w1\n"), 0600))
			require.NoError(t, os.MkdirAll(filepath.Join(d.Dir, "jobs", job), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(d.Dir, "jobs", job, "JOB.md"), []byte("staged\n"), 0600))
			now := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
			l := nudgeLoop(d)
			l.recoverNudgeDebt(now)
			require.Len(t, l.nudgeLedger.Records, 1)
			d.status.HeldKnown = true
			d.heldCards = []HeldCard{{Job: job}}
			switch state {
			case "unknown":
				d.status.HeldKnown = false
			case "removed":
				require.NoError(t, os.Chtimes(brief, now.Add(-time.Hour), now.Add(-time.Hour)))
				l.inboxStep(now)
				require.NoFileExists(t, brief, "current empty row retires the stale staged brief")
			case "refresh-error":
				d.status.InboxError = "server unavailable"
			case "missing-brief":
				require.NoError(t, os.Remove(brief))
			}
			if state == "current" {
				require.True(t, l.startOwedDealt(now))
				got := <-l.results
				l.batchDone(result{t: got.t, exit: 0}, now)
				return
			}
			assert.False(t, l.startOwedDealt(now), "stale recovered debt cannot instruct batch work")
			l.busy = &turn{nudge: true, attempt: 1, dealt: []string{deferredBrief}}
			l.retry = now.Add(-time.Second)
			assert.True(t, l.retryDeferred(now), "due advisory remains held")
			deliver.mu.Lock()
			assert.Empty(t, deliver.texts)
			deliver.mu.Unlock()
			var ledger nudgeLedger
			_, err := read(filepath.Join(d.Dir, NudgeDebtFile), &ledger)
			require.NoError(t, err)
			require.Len(t, ledger.Records, 1)
			assert.Equal(t, nudgePending, ledger.Records[0].Phase, "row uncertainty cannot mark delivery inflight")
		})
	}
}

func TestPassiveBatchNudgeRemainsPendingWithoutDelivery(t *testing.T) {
	t.Parallel()
	var records []string
	w := &wordBox{known: true}
	deliver := &captureDeliver{}
	d := w.daemon(t, &records, deliver)
	l := nudgeLoop(d)
	l.passive = true
	now := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
	l.dealt = []string{deferredBrief}
	l.persistStagedBriefs(now)
	assert.False(t, l.startOwedDealt(now))
	var ledger nudgeLedger
	_, err := read(filepath.Join(d.Dir, NudgeDebtFile), &ledger)
	require.NoError(t, err)
	require.Len(t, ledger.Records, 1)
	assert.Equal(t, nudgePending, ledger.Records[0].Phase)
	assert.Nil(t, l.busy)
	deliver.mu.Lock()
	assert.Empty(t, deliver.texts)
	deliver.mu.Unlock()
	restarted := nudgeLoop(d)
	restarted.recoverNudgeDebt(now)
	assert.Equal(t, []string{deferredBrief}, restarted.owedBriefs)
}

func TestMixedNudgeKeepsWithdrawnDebtWithoutStarvingCurrentWork(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{nudgePending, nudgeDeferred} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			var records []string
			deliver := &captureDeliver{}
			w := &wordBox{known: true}
			d := w.daemon(t, &records, deliver)
			d.Held = func(context.Context) (Row, error) { return Row{}, nil }
			d.status.HeldKnown = true
			d.heldCards = []HeldCard{{Job: "card.w2~15"}}
			brief := filepath.Join(d.Dir, "inbox", "card.w2~15", "BRIEF.md")
			require.NoError(t, os.MkdirAll(filepath.Dir(brief), 0700))
			require.NoError(t, os.WriteFile(brief, []byte("STATUS: nova-sprint card card.w2\n"), 0600))
			l := nudgeLoop(d)
			now := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
			l.dealt = []string{deferredBrief, deferredBriefStarted}
			l.persistStagedBriefs(now)
			if phase == nudgeDeferred {
				_, err := l.markNudgePhase(1, nudgeDeferred, nil, now)
				require.NoError(t, err)
				l.busy = &turn{nudge: true, attempt: 1, dealt: []string{deferredBrief, deferredBriefStarted}}
				l.retry = now.Add(-time.Second)
				require.True(t, l.retryDeferred(now))
				require.Nil(t, l.busy, "durable deferred turn is rebuilt without withdrawn text")
			}
			before := l.nudgeLedger.clone()
			original := d.Dir
			blocked := filepath.Join(original, "blocked-ledger-root")
			require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0600))
			d.Dir = blocked
			_, splitErr := l.markNudgePhase(1, nudgeInflight, []string{deferredBriefStarted}, now)
			require.Error(t, splitErr, "failed split save must retain the original whole record")
			assert.Equal(t, before, l.nudgeLedger)
			d.Dir = original
			require.True(t, l.startOwedDealt(now))
			got := <-l.results
			assert.NotContains(t, got.t.text, "inbox/card.w1~15/")
			assert.Contains(t, got.t.text, "inbox/card.w2~15/")
			l.batchDone(result{t: got.t, exit: 0}, now)
			var ledger nudgeLedger
			_, err := read(filepath.Join(d.Dir, NudgeDebtFile), &ledger)
			require.NoError(t, err)
			require.Len(t, ledger.Records, 2)
			assert.Equal(t, phase, ledger.Records[0].Phase)
			assert.Equal(t, []string{deferredBrief}, ledger.Records[0].Briefs)
			assert.Equal(t, nudgeAccepted, ledger.Records[1].Phase)
			assert.Equal(t, []string{deferredBriefStarted}, ledger.Records[1].Briefs)
			assert.False(t, l.startOwedDealt(now), "no replay of current accepted or withdrawn retained debt")
		})
	}
}

func TestWithdrawnDeferredNudgeFreesBatchSlotForOrdinaryRequest(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{nudgeDeferred, nudgeInflight} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			var records []string
			deliver := &captureDeliver{}
			w := &wordBox{known: true}
			d := w.daemon(t, &records, deliver)
			now := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
			d.Store = bustest.NewFake(now, d.Friend)
			d.Held = func(context.Context) (Row, error) { return Row{Reads: true}, nil }
			d.status.HeldKnown = true
			l := nudgeLoop(d)
			l.b = &bus.Bus{Store: d.Store}
			l.dealt = []string{deferredBrief}
			l.persistStagedBriefs(now)
			_, err := l.markNudgePhase(1, phase, nil, now)
			require.NoError(t, err)
			message := bus.Message{ID: "ordinary", Kind: bus.KindRequest, Subject: "ordinary control", Body: "answer ordinary request"}
			l.hand = []bus.Entry{{Stream: bus.StreamOf(d.Friend), Entry: "req-1", Fields: message.Fields()}}
			l.busy = &turn{nudge: true, attempt: 1, dealt: []string{deferredBrief}}
			l.retry = now.Add(-time.Second)
			require.True(t, l.retryDeferred(now))
			if phase == nudgeInflight {
				assert.NotNil(t, l.busy, "unknown inflight cannot be discarded")
				return
			}
			require.Nil(t, l.busy, "durable withdrawn advice cannot block ordinary requests")
			l.startBatch(now)
			got := <-l.results
			assert.Contains(t, got.t.text, "answer ordinary request")
			assert.NotContains(t, got.t.text, "Read each BRIEF.md and start")
			var ledger nudgeLedger
			_, err = read(filepath.Join(d.Dir, NudgeDebtFile), &ledger)
			require.NoError(t, err)
			require.Len(t, ledger.Records, 1)
			assert.Equal(t, nudgeDeferred, ledger.Records[0].Phase)
		})
	}
}

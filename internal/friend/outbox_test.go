package friend

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first inbox/outbox pass runs before lane recovery. Reports cannot bypass
// unknown durable state, Started claims awaiting the machine word, or live
// native runs awaiting a free adoption lane (FriendLaneCompletion).
func TestRestartOutboxWaitsForDurableLaneSettlement(t *testing.T) {
	t.Parallel()
	for _, test := range []string{"unreadable state", "unknown machine", "live runs"} {
		t.Run(test, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				dir := cardDirFixture(t, [][2]string{{"c1", "working"}, {"c2", "working"}}, []string{"c1", "c2"}, nil)
				h := &lanesHarness{dir: dir, active: map[string]int{}}
				r, state := laneRig(t, h, 1)
				state.Sessions = map[int]string{1: "ses_1"}
				state.Started = map[string]Started{}
				row := &twinRow{}
				row.set(workCard("c1", "working"), workCard("c2", "working"))
				r.d.Held = row.held
				f := &finishes{}
				loaded := false
				r.d.LoadLanes = func() (LaneState, error) {
					loaded = true
					if test == "unreadable state" {
						return LaneState{}, errors.New("state unavailable")
					}
					return *state, nil
				}
				if test == "unreadable state" {
					r.d.SaveLanes = func(LaneState) error { return errors.New("state unavailable") }
				}
				r.d.Finish = func(ctx context.Context, argv []string) error {
					assert.True(t, loaded, "outbox cannot finish before the durable state read")
					return f.finish(ctx, argv)
				}
				for _, id := range []string{"c1", "c2"} {
					job := id + "~15"
					card := Card{ID: id, Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
					state.Started[job] = Started{Lane: 1, Card: card, At: t0, RunID: job, Owner: "old"}
					require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
					require.NoError(t, writeRunReceipt(dir, job, runReceipt{RunID: job, PID: 4242, Identity: "birth"}))
					require.NoError(t, os.WriteFile(laneMarkPath(dir, job), []byte(LaneMarkRunningRun("old", t0, job)), 0o644))
					outboxReport(t, dir, job, "Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n\nDone.\n")
				}
				var mu sync.Mutex
				alive, known := true, test != "unknown machine"
				r.d.MachineKnown = func() bool { mu.Lock(); defer mu.Unlock(); return known }
				r.d.ProcessAlive = func(int) bool { mu.Lock(); defer mu.Unlock(); return alive }
				r.d.ProcessIdentity = func(int) string {
					mu.Lock()
					defer mu.Unlock()
					if alive {
						return "birth"
					}
					return ""
				}
				ended := make(chan struct{})
				r.d.WaitProcess = func(ctx context.Context, _ int) bool {
					select {
					case <-ended:
						return true
					case <-ctx.Done():
						return false
					}
				}
				r.at[4] = func() {
					assert.Empty(t, f.got(), "reports retain canonical capacity while recovery is unsettled")
					assert.Len(t, state.Started, 2, "both jobs remain durable, including the one waiting for a lane")
				}
				r.at[6] = func() {
					mu.Lock()
					known = true
					alive = false
					mu.Unlock()
					close(ended)
				}
				r.run(t, 12)
				if test == "unreadable state" {
					assert.Empty(t, f.got(), "failed state reads never authorize report completion")
					assert.Len(t, state.Started, 2)
				} else {
					var cards []string
					for _, argv := range f.got() {
						cards = append(cards, argv[3])
					}
					assert.ElementsMatch(t, []string{"c1@1", "c2@1"}, cards, "each report finishes once after safe settlement")
					assert.Empty(t, state.Started)
				}
				for _, id := range []string{"c1", "c2"} {
					assert.FileExists(t, filepath.Join(dir, "outbox", id+"~15", "REPORT.md"))
				}
			})
		})
	}
}

func TestBatchRestartOutboxPreservesPriorOneShotDebt(t *testing.T) {
	t.Parallel()
	for _, unreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "known claims", true: "partial failed read"}[unreadable], func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.passive = true
			row, f := &twinRow{}, &finishes{}
			row.set(workCard("prior", "working"), workCard("cancelled", "working"), workCard("batch", "working"))
			r.d.Held, r.d.Finish = row.held, f.finish
			for _, id := range []string{"prior", "cancelled", "batch"} {
				outboxReport(t, r.d.Dir, id+"~15", "Verdict: FAIL\n\nFinished.\n")
			}
			prior := Card{ID: "prior", Outbox: filepath.Join(r.d.Dir, "outbox", "prior~15")}
			state := LaneState{
				Started:     map[string]Started{"prior~15": {Lane: 1, Card: prior, At: t0, Pid: 4242, RunID: "prior-run"}},
				StopReturns: []StopReturn{{Job: "cancelled~15", Card: "cancelled", Ended: true}},
			}
			require.NoError(t, os.MkdirAll(filepath.Join(r.d.Dir, "jobs", "prior~15"), 0o755))
			require.NoError(t, writeRunReceipt(r.d.Dir, "prior~15", runReceipt{RunID: "prior-run", PID: 4242, Identity: "birth"}))
			alive := true
			r.d.ProcessIdentity = func(int) string {
				if alive {
					return "birth"
				}
				return ""
			}
			loads := 0
			r.d.LoadLanes = func() (LaneState, error) {
				loads++
				if unreadable {
					return state, errors.New("partial durable read")
				}
				return state, nil
			}
			r.at[3] = func() {
				if unreadable {
					assert.Empty(t, f.got(), "unknown claims hold even an unrelated batch report")
				} else {
					require.Len(t, f.got(), 1)
					assert.Equal(t, "batch@1", f.got()[0][3], "unrelated batch reports remain finishable")
				}
				// Simulate recovery's durable settlement between passes. The
				// outbox observes it without adopting the old one-shot sessions.
				alive = false
				delete(state.Started, "prior~15")
				state.StopReturns[0].Result = "ok"
				row.set(workCard("prior", "working"), workCard("batch", "working"))
			}
			r.run(t, 5)
			assert.GreaterOrEqual(t, loads, 5, "durable clearance must be observed on later batch passes")
			if unreadable {
				assert.Empty(t, f.got())
			} else {
				var cards []string
				for _, argv := range f.got() {
					cards = append(cards, argv[3])
				}
				assert.ElementsMatch(t, []string{"prior@1", "batch@1"}, cards, "a cancelled card is never finished from its old report")
			}
			assert.Equal(t, ModeBatch, r.last().Mode)
		})
	}
}

// The daemon reads every outbox job (the night of 2026-10-05: eight working cards whose
// outbox REPORT.md said Verdict: LAND with a Head sat unread for two hours, because the
// daemon finished only the jobs its own lanes ran, and the coordinator's stopgap had
// written those briefs). Here no brief is the daemon's: each is put in her inbox by
// another hand before the daemon starts, and its card is on her row.
func outboxReport(t *testing.T, dir, job, report string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "REPORT.md"), []byte(report), 0o644))
}

func TestTheDaemonFinishesAReportItDidNotStage(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	f := &finishes{}
	r.d.Finish = f.finish

	const head = "0123456789abcdef0123456789abcdef01234567"
	land, hold, fail := workCard("landed.w1", "working"), workCard("held.w1", "working"), workCard("failed.w1", "working")
	gen := workCard("again.w1", "working")
	gen.Job, gen.Gen = "again.w1~15.g3", 3
	silent, ready := workCard("silent.w1", "working"), workCard("ready.w1", "ready")
	for _, c := range []HeldCard{land, hold, fail, gen, silent, ready} {
		inboxJob(t, dir, c.Job, c.Brief) // the coordinator's stopgap wrote them: none is the daemon's
	}
	long := "the bench went red.\n" + strings.Repeat("x", 700) + "\n"
	outboxReport(t, dir, land.Job, "Verdict: LAND\nHead: "+head+"\n\nThe card is done.\n")
	outboxReport(t, dir, hold.Job, "Verdict: HOLD\nHead: "+head+"\n\nno push\n")
	outboxReport(t, dir, fail.Job, "Verdict: FAIL\n\n"+long)
	// A worker report wins even if the runner log also says END report=no.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner.log"), []byte("t START "+fail.Job+" tier=heavy\nt END "+fail.Job+" exit=0 report=no\n"), 0o644))
	outboxReport(t, dir, gen.Job, "**Verdict:** land\nHead: "+strings.ToUpper(head)+"\n\nthird time.\n")
	outboxReport(t, dir, silent.Job, "I am still working on it.\n")
	outboxReport(t, dir, ready.Job, "Verdict: LAND\nHead: "+head+"\n")
	outboxReport(t, dir, "gone.w1~15", "Verdict: LAND\nHead: "+head+"\n") // its card is not on her row
	outboxReport(t, dir, "not-a-job", "Verdict: LAND\nHead: "+head+"\n")  // no <work>~<epoch> name
	row.set(land, hold, fail, gen, silent, ready)

	r.run(t, 3)

	got := map[string][]string{}
	for _, argv := range f.got() {
		require.GreaterOrEqual(t, len(argv), 4, "%v", argv)
		got[argv[3]] = argv
	}
	require.Len(t, got, 4, "one finish per working card with a verdict, sent once over three loops: %v", f.got())
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "landed.w1@1", "--epoch", "15", "--head", head, "--branch", "sprint/landed.w1.g1.e15", "--op", flagValue(got["landed.w1@1"], "--op"), "--report", "friend bob LAND: The card is done."}, got["landed.w1@1"])
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "again.w1@3", "--epoch", "15", "--head", head, "--branch", "sprint/again.w1.g1.e15", "--op", flagValue(got["again.w1@3"], "--op"), "--report", "friend bob LAND: third time."}, got["again.w1@3"], "a job's generation is its finish's; a sha is read in lower case")
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "held.w1@1", "--epoch", "15", "--failed", "--head", head, "--branch", "sprint/held.w1.g1.e15", "--op", flagValue(got["held.w1@1"], "--op"), "--report", "friend bob HOLD: Verdict: HOLD Head: " + head + " no push"}, got["held.w1@1"])
	failed := got["failed.w1@1"]
	require.Len(t, failed, 13)
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "failed.w1@1", "--epoch", "15", "--failed", "--branch", "sprint/failed.w1.g1.e15", "--op", flagValue(failed, "--op"), "--report"}, failed[:12])
	assert.Equal(t, "friend bob FAIL: "+oneLine(("Verdict: FAIL\n\n" + long)[:600], 600), failed[12], "a failed finish carries the report's first 600 characters")
	gotReport, err := os.ReadFile(filepath.Join(dir, "outbox", fail.Job, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, "Verdict: FAIL\n\n"+long, string(gotReport), "the no-report marker never replaces a worker report")

	count := func(sub string) int {
		n := 0
		for _, l := range r.records {
			if strings.Contains(l, sub) {
				n++
			}
		}
		return n
	}
	assert.Equal(t, 1, count("outbox: left outbox/silent.w1~15/REPORT.md: it has no Verdict line"), "a report with no verdict is noted once: %v", r.records)
	assert.Equal(t, 1, count("outbox: left outbox/ready.w1~15/REPORT.md: card ready.w1 is ready on her row, not working"), "%v", r.records)
	assert.Equal(t, 1, count("outbox: left outbox/gone.w1~15/REPORT.md: refused: card gone.w1 is not on her row, no longer hers; no row the daemon reads says who holds it now (nova-sprint view coordinator does)"), "%v", r.records)
	assert.Equal(t, 0, count("not-a-job"), "a directory no card names is not hers to finish")
	assert.Equal(t, 4, count("outbox: finished card "), "one line per finish: %v", r.records)

	// she writes the verdict: the next pass finishes it; a finished job is not noted after it leaves her row
	outboxReport(t, dir, silent.Job, "Verdict: FAIL\n\nstuck.\n")
	row.set(silent, ready)
	r.run(t, 2)
	assert.Len(t, f.got(), 5)
	assert.Equal(t, "silent.w1@1", f.got()[4][3])
	assert.Equal(t, 0, count("card landed.w1 is not on her row"), "a job the daemon finished is not noted when its card leaves her row")
}

func TestReportVerdictReadsTheFriendsWords(t *testing.T) {
	t.Parallel()
	for report, want := range map[string][2]string{
		"Verdict: LAND\nHead: ABC\n":        {"LAND", "abc"},
		"## Verdict: hold.\n":               {"HOLD", ""},
		"- **Verdict**: FAIL (no bench)\n":  {"FAIL", ""},
		"no verdict here\nHead: abc\n":      {"", "abc"},
		"Head: `abc`, pushed\nVerdict: L\n": {"L", "abc"},
	} {
		v, h := reportVerdict(report)
		assert.Equal(t, want, [2]string{v, h}, report)
	}
}

// The daemon's duty is nova-sprint collect's for her own tree: a LAND finishes only at
// origin's tip of the card's branch (refused naming the branch otherwise), and a lane her
// runner ENDed with no report (and no LIMIT) is a dead lane, its REPORT.md atomically
// claimed as a harness fault and its same attempt returned for a fresh generation.
func TestTheDaemonFinishesADeadLaneAndALandOnlyAtOriginsTip(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	f := &finishes{}
	r.d.Finish = f.finish
	const head, other = "0123456789abcdef0123456789abcdef01234567", "fedcba9876543210fedcba9876543210fedcba98"
	r.d.Tip = func(_ context.Context, repo, branch string) (string, error) {
		assert.Equal(t, "mas-bandwidth/nova-tools", repo)
		if branch == "sprint/off.w1.g1.e15" {
			return other, nil
		}
		return head, nil
	}
	on, off, dead, limit := workCard("on.w1", "working"), workCard("off.w1", "working"), workCard("dead.w1", "working"), workCard("limit.w1", "working")
	for _, c := range []*HeldCard{&on, &off, &dead, &limit} {
		c.Repo = "mas-bandwidth/nova-tools"
		inboxJob(t, dir, c.Job, c.Brief)
	}
	outboxReport(t, dir, on.Job, "Verdict: LAND\nHead: "+head+"\n\nOn the tip.\n")
	outboxReport(t, dir, off.Job, "Verdict: LAND\nHead: "+head+"\n\nNot pushed.\n")
	// the runner's runs wrote RESULT.md and no REPORT.md: no lane of the daemon takes them
	for _, job := range []string{dead.Job, limit.Job} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "RESULT.md"), []byte("done\n"), 0o644))
	}
	end := "2026-10-06 07:10:00 AM END dead.w1~15 model=m exit=1 wall=600s report=no"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner.log"), []byte(strings.Join([]string{
		"2026-10-06 07:00:00 AM START dead.w1~15 tier=heavy model=m", end,
		"2026-10-06 07:00:00 AM START limit.w1~15 tier=heavy model=m",
		"2026-10-06 07:01:00 AM LIMIT limit.w1~15 model=m until=later: limit",
		"2026-10-06 07:01:00 AM END limit.w1~15 model=m exit=1 wall=60s report=no",
	}, "\n")+"\n"), 0o644))
	row.set(on, off, dead, limit)

	r.run(t, 3)

	got := map[string][]string{}
	for _, argv := range f.got() {
		got[argv[3]] = argv
	}
	require.Len(t, got, 2, "the LAND on origin's tip and the dead lane, once each: %v", f.got())
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "on.w1@1", "--epoch", "15", "--head", head, "--branch", "sprint/on.w1.g1.e15", "--op", flagValue(got["on.w1@1"], "--op"), "--report", "friend bob LAND: On the tip."}, got["on.w1@1"])
	d := got["dead.w1@1"]
	require.Len(t, d, 11)
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "dead.w1@1", "--epoch", "15", "--harness-fault", "--op", flagValue(d, "--op"), "--report"}, d[:10])
	assert.Contains(t, d[10], "harness-fault: no report; Verdict: HARNESS-FAULT Runner-END: "+end)
	report, err := os.ReadFile(filepath.Join(dir, "outbox", dead.Job, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, DeadLaneReport("bob", dead.Job, end), string(report))
	_, err = os.Stat(filepath.Join(dir, "outbox", limit.Job, "REPORT.md"))
	assert.True(t, os.IsNotExist(err), "a run stopped at its usage limit is run again, never dead")
	said := strings.Join(r.records, "\n")
	assert.Contains(t, said, "return=harness-fault", "the daemon records a return, not a successful finish")
	assert.Contains(t, said, "outbox: left outbox/off.w1~15/REPORT.md: Head "+head+" is not origin's tip of sprint/off.w1.g1.e15, "+other)
}

func TestTheDaemonLeavesAStaleHarnessFaultMarkerForARestartedRunner(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	f := &finishes{}
	r.d.Finish = f.finish
	card := workCard("restart.w1", "working")
	inboxJob(t, dir, card.Job, card.Brief)
	row.set(card)
	outboxReport(t, dir, card.Job, DeadLaneReport("bob", card.Job, "t END "+card.Job+" exit=0 report=no"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner.log"), []byte("t START "+card.Job+" tier=heavy\nt END "+card.Job+" exit=0 report=no\nt START "+card.Job+" tier=heavy\n"), 0o644))
	r.run(t, 2)
	assert.Empty(t, f.got(), "a persisted marker cannot return a restarted job")
	assert.Contains(t, strings.Join(r.records, "\n"), "harness-fault marker is stale")
	outboxReport(t, dir, card.Job, "Verdict: FAIL\n\nThe worker reported a failure.\n")
	r.run(t, 2)
	require.Len(t, f.got(), 1)
	assert.Contains(t, f.got()[0], "--failed", "the worker's report keeps its failed disposition")
}

func TestRunnerEndedReadsTheJobsLastEventAsCollectDoes(t *testing.T) {
	t.Parallel()
	for log, want := range map[string]bool{
		"": false,
		"t START j~1 x\nt END j~1 exit=0 report=Verdict: LAND":     false,
		"t START j~1 x\nt END j~1 exit=0 report=no":                true,
		"t START j~1 x\nt END j~1 exit=0 report=no\nt START j~1 x": false,
		"t START j~1 x\nt LIMIT j~1 x\nt END j~1 exit=1 report=no": false,
		"t START j~10 x\nt END j~10 exit=1 report=no":              false,
	} {
		_, dead := RunnerEnded(log, "j~1")
		assert.Equal(t, want, dead, "%q", log)
	}
}

func TestOutboxFinishRetriesWithBoundedBackoff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	card := workCard("retry.w1", "working")
	const report = "Verdict: FAIL\n\nThe report stays durable.\n"
	inboxJob(t, r.d.Dir, card.Job, card.Brief)
	outboxReport(t, r.d.Dir, card.Job, report)
	r.d.heldCards = []HeldCard{card}
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
	var attempts [][]string
	recovered := false
	r.d.Finish = func(_ context.Context, argv []string) error {
		attempts = append(attempts, append([]string(nil), argv...))
		if !recovered {
			return errors.New("the server is unavailable")
		}
		return nil
	}
	now := t0
	l.outboxStep(now)
	require.Len(t, attempts, 1)
	for i, delay := range []time.Duration{BeatEvery, 2 * BeatEvery, 4 * BeatEvery, 8 * BeatEvery, 16 * BeatEvery, 32 * BeatEvery, OutboxRetry, OutboxRetry} {
		assert.Equal(t, delay, r.d.outbox.tried[card.Job].delay)
		l.outboxStep(now.Add(delay - time.Nanosecond))
		assert.Len(t, attempts, i+1, "no retry before its bound")
		assert.False(t, r.d.outbox.finished[card.Job], "failure never acknowledges completion")
		assert.Equal(t, "working", r.d.heldCards[0].Col, "canonical ownership remains held")
		now = now.Add(delay)
		l.outboxStep(now)
		require.Len(t, attempts, i+2)
		assert.Equal(t, attempts[0], attempts[i+1], "every retry carries the exact payload and operation ID")
	}
	recovered = true
	l.outboxStep(now.Add(OutboxRetry))
	require.Len(t, attempts, 10)
	assert.True(t, r.d.outbox.finished[card.Job])
	assert.Empty(t, r.d.outbox.tried)
	l.outboxStep(now.Add(2 * OutboxRetry))
	assert.Len(t, attempts, 10, "an acknowledged finish is never sent twice")
	got, there, err := readReport(filepath.Join(r.d.Dir, "outbox"), card.Job)
	require.NoError(t, err)
	assert.True(t, there)
	assert.Equal(t, report, got)
}

func TestOutboxFinishReplaysALostAnswerAcrossRestart(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	card := workCard("replay.w1", "working")
	const report = "Verdict: FAIL\n\nA finish with a lost answer.\n"
	inboxJob(t, r.d.Dir, card.Job, card.Brief)
	outboxReport(t, r.d.Dir, card.Job, report)
	r.d.heldCards = []HeldCard{card}
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
	operations := map[string][]string{}
	applied, attempts := 0, 0
	r.d.Finish = func(_ context.Context, argv []string) error {
		attempts++
		op := flagValue(argv, "--op")
		require.NotEmpty(t, op)
		if prior, ok := operations[op]; ok {
			assert.Equal(t, prior, argv, "the server replays exactly the operation that applied")
			return nil
		}
		operations[op] = append([]string(nil), argv...)
		applied++
		return errors.New("the answer was lost after apply")
	}
	l.outboxStep(t0)
	assert.False(t, r.d.outbox.finished[card.Job])
	assert.NotContains(t, strings.Join(r.records, "\n"), "outbox: finished card")
	assert.NotContains(t, strings.Join(r.records, "\n"), "finish was not taken")
	assert.Contains(t, strings.Join(r.records, "\n"), "finish result is not confirmed")
	// The report is durable; a fresh daemon can reset the volatile retry delay.
	r.d.outbox = outboxState{}
	l.outboxStep(t0.Add(BeatEvery))
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 1, applied, "restart cannot apply completion twice")
	assert.True(t, r.d.outbox.finished[card.Job])
	l.outboxStep(t0.Add(2 * BeatEvery))
	assert.Equal(t, 2, attempts)
	argv := OutboxFinishArgv(r.d.Friend, Card{ID: card.Card, Outbox: filepath.Join(r.d.Dir, "outbox", card.Job)}, "FAIL", "", card.Branch, report)
	changed := OutboxFinishArgv(r.d.Friend, Card{ID: card.Card, Outbox: filepath.Join(r.d.Dir, "outbox", card.Job)}, "FAIL", "", card.Branch, report+"Corrected.\n")
	assert.NotEqual(t, flagValue(argv, "--op"), flagValue(changed, "--op"), "a corrected payload cannot replay the old finish")
}

func TestOutboxFinishRetryRechecksCanonicalOwnership(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	card := workCard("taken.w1", "working")
	inboxJob(t, r.d.Dir, card.Job, card.Brief)
	outboxReport(t, r.d.Dir, card.Job, "Verdict: FAIL\n\nStill durable.\n")
	r.d.heldCards = []HeldCard{card}
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
	attempts := 0
	r.d.Finish = func(context.Context, []string) error {
		attempts++
		return errors.New("server unavailable")
	}
	l.outboxStep(t0)
	r.d.heldCards = nil
	l.outboxStep(t0.Add(BeatEvery))
	assert.Equal(t, 1, attempts, "a report cannot finish a card after it leaves her row")
	assert.False(t, r.d.outbox.finished[card.Job])
}

// A fresh sprint delivers a bare epoch-zero job. Its report is finished only
// after the current friend-cards row binds that exact job and generation.
func TestEpochZeroOutboxFinishesOnlyTheCurrentExactJob(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, job string
		gen       int
	}{
		{name: "first generation", job: "card.w1", gen: 1},
		{name: "resumed generation", job: "card.w1.g2", gen: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			h := HeldCard{Card: "card.w1", Job: tc.job, Col: "working", Kind: "work", Gen: tc.gen, Epoch: 0}
			r.d.heldCards = []HeldCard{h}
			r.d.status.HeldFrom = FromCards
			outboxReport(t, r.d.Dir, tc.job, "Verdict: FAIL\n\nThe native run completed.\n")
			var sent [][]string
			r.d.Finish = func(_ context.Context, argv []string) error {
				sent = append(sent, append([]string(nil), argv...))
				return nil
			}
			l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
			l.outboxStep(t0)
			require.Len(t, sent, 1)
			assert.Equal(t, "card.w1@"+strconv.Itoa(tc.gen), sent[0][3])
			assert.Equal(t, "0", flagValue(sent[0], "--epoch"))
			assert.True(t, r.d.outbox.finished[tc.job])
			l.outboxStep(t0.Add(BeatEvery))
			assert.Len(t, sent, 1, "an acknowledged epoch-zero report is not replayed")
		})
	}
}

func TestEpochZeroOutboxNeverInfersAnOldOrUnverifiedJob(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, card, heldJob, reportJob, source string
		epoch                                  uint64
		gen                                    int
		duplicate                              bool
	}{
		{name: "old epoch after clear", heldJob: "card.w1~1", reportJob: "card.w1", source: FromCards, epoch: 1},
		{name: "old generation", heldJob: "card.w1.g2", reportJob: "card.w1", source: FromCards, gen: 2},
		{name: "bare job with stale epoch metadata", heldJob: "card.w1", reportJob: "card.w1", source: FromCards, epoch: 1},
		{name: "bare job with wrong generation metadata", heldJob: "card.w1", reportJob: "card.w1", source: FromCards, gen: 2},
		{name: "bare job with mismatched card", card: "other.w1", heldJob: "card.w1", reportJob: "card.w1", source: FromCards},
		{name: "noncanonical zero alias", heldJob: "card.w1", reportJob: "card.w1~0", source: FromCards},
		{name: "malformed qualified current claim", card: "card.w1~bad", heldJob: "card.w1~bad", reportJob: "card.w1~bad", source: FromCards},
		{name: "row came from fallback view", heldJob: "card.w1", reportJob: "card.w1", source: FromView},
		{name: "no current row", reportJob: "card.w1", source: FromCards},
		{name: "duplicate current claim", heldJob: "card.w1", reportJob: "card.w1", source: FromCards, duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			if tc.heldJob != "" {
				gen := max(tc.gen, 1)
				card := cmp.Or(tc.card, "card.w1")
				r.d.heldCards = []HeldCard{{Card: card, Job: tc.heldJob, Col: "working", Kind: "work", Gen: gen, Epoch: tc.epoch}}
				if tc.duplicate {
					r.d.heldCards = append(r.d.heldCards, r.d.heldCards[0])
				}
			}
			r.d.status.HeldFrom = tc.source
			outboxReport(t, r.d.Dir, tc.reportJob, "Verdict: FAIL\n\nAn old report.\n")
			calls := 0
			r.d.Finish = func(context.Context, []string) error { calls++; return nil }
			l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
			l.outboxStep(t0)
			assert.Zero(t, calls, "unbound reports cannot finish current work")
		})
	}
}

func TestEpochZeroDeadLaneReportCanFinishAfterVerifiedRunnerEnd(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.heldCards = []HeldCard{{Card: "card.w1", Job: "card.w1", Col: "working", Kind: "work", Gen: 1, Epoch: 0}}
	r.d.status.HeldFrom = FromCards
	require.NoError(t, os.MkdirAll(filepath.Join(r.d.Dir, "outbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(r.d.Dir, "runner.log"), []byte("t START card.w1 x\nt END card.w1 exit=0 report=no\n"), 0o600))
	var sent [][]string
	r.d.Finish = func(_ context.Context, argv []string) error {
		sent = append(sent, append([]string(nil), argv...))
		return nil
	}
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
	l.outboxStep(t0)
	require.Len(t, sent, 1)
	assert.Equal(t, "card.w1@1", sent[0][3])
	assert.Equal(t, "0", flagValue(sent[0], "--epoch"))
	assert.Contains(t, sent[0], "--harness-fault")
	assert.FileExists(t, filepath.Join(r.d.Dir, "outbox", "card.w1", "REPORT.md"))
}

package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pausedTakeAcquire struct {
	*Mem
	once    sync.Once
	verb    string
	entered chan struct{}
	release chan struct{}
}

func (b *pausedTakeAcquire) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if op.Verb == b.verb {
		b.once.Do(func() {
			close(b.entered)
			<-b.release
		})
	}
	return b.Mem.Acquire(ctx, gen, op)
}

func TestStopFencesATakeThatReadRunningBeforeStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	b := &pausedTakeAcquire{Mem: h.m, verb: "take", entered: make(chan struct{}), release: make(chan struct{})}
	h.st.B = b
	type done struct {
		res Result
		err error
	}
	finished := make(chan done, 1)
	go func() {
		res, err := h.st.Run(h.ctx, TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
		finished <- done{res, err}
	}()
	<-b.entered // the take planned on RUNNING but has not acquired its commit fence
	_, _, _, err := h.st.StopUntil(h.ctx, "cancel owned children", h.now.Add(time.Hour))
	require.NoError(t, err)
	close(b.release)
	got := <-finished
	require.NoError(t, got.err)
	require.NotEmpty(t, got.res.Refused, "the stale take must re-read STOP and refuse")
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(wc.ID).Col)
	h.clean("stop fenced stale take")
}

func TestOldTickPartCannotCommitAcrossStopAndRestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	observed, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	b := &pausedTakeAcquire{Mem: h.m, verb: "tick stale run", entered: make(chan struct{}), release: make(chan struct{})}
	h.st.B = b
	step := Step{Verb: "tick stale run", Halts: true, TickRunSeq: &observed.RunSeq,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "tick-stale-probe", Who: sprint.MachineActor, At: s.Now, What: "old tick note"}}}
		}}
	type done struct {
		res Result
		err error
	}
	finished := make(chan done, 1)
	go func() {
		res, err := h.st.Run(h.ctx, step)
		finished <- done{res, err}
	}()
	select {
	case <-b.entered:
	case got := <-finished:
		t.Fatalf("old tick part finished before acquire: result=%+v err=%v", got.res, got.err)
	}
	_, _, _, err = h.st.StopUntil(h.ctx, "operator cancellation", h.now.Add(time.Hour))
	require.NoError(t, err)
	_, restarted, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	require.Greater(t, restarted.RunSeq, observed.RunSeq)
	close(b.release)
	got := <-finished
	require.NoError(t, got.err)
	assert.True(t, got.res.StaleRun)
	assert.Zero(t, got.res.Notes, "the old tick part cannot commit in the new run")
}

func TestManualStopWinsAnOlderTickCauseStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	observed, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	b := &pausedTakeAcquire{Mem: h.m, verb: "machine cause lock", entered: make(chan struct{}), release: make(chan struct{})}
	h.st.B = b
	type done struct {
		changed bool
		err     error
	}
	finished := make(chan done, 1)
	go func() {
		_, _, changed, err := h.st.stopWithCause(h.ctx, sprint.DoneCause, observed.RunSeq)
		finished <- done{changed, err}
	}()
	<-b.entered // tick saw RUNNING but has not acquired the shared fence
	_, _, _, err = h.st.StopUntil(h.ctx, "operator cancellation", h.now.Add(time.Hour))
	require.NoError(t, err)
	close(b.release)
	got := <-finished
	require.NoError(t, got.err)
	assert.False(t, got.changed, "an old tick may not replace the explicit stop")
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "operator cancellation", m.Reason)
	assert.Empty(t, m.Cause)
}

func TestOldTickCannotStopAnExplicitlyRestartedRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	observed, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	b := &pausedTakeAcquire{Mem: h.m, verb: "machine cause lock", entered: make(chan struct{}), release: make(chan struct{})}
	h.st.B = b
	type done struct {
		changed bool
		err     error
	}
	finished := make(chan done, 1)
	go func() {
		_, _, changed, err := h.st.stopWithCause(h.ctx, sprint.DoneCause, observed.RunSeq)
		finished <- done{changed, err}
	}()
	<-b.entered
	_, _, _, err = h.st.StopUntil(h.ctx, "operator cancellation", h.now.Add(time.Hour))
	require.NoError(t, err)
	_, restarted, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	require.Greater(t, restarted.RunSeq, observed.RunSeq)
	close(b.release)
	got := <-finished
	require.NoError(t, got.err)
	assert.False(t, got.changed, "old tick cannot stop the new run")
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.True(t, m.Running())
	assert.Equal(t, restarted.RunSeq, m.RunSeq)
	var doneRes, fundsRes TickResult
	require.NoError(t, h.st.stopDone(h.ctx, sprint.Note{What: "old done"}, observed.RunSeq, &doneRes))
	require.NoError(t, h.st.stopFor(h.ctx, sprint.FundsCause, "old funds stop", observed.RunSeq, &fundsRes))
	assert.Equal(t, Running, doneRes.State)
	assert.Empty(t, doneRes.Done)
	assert.NotEmpty(t, doneRes.Stale)
	assert.Equal(t, Running, fundsRes.State)
	assert.Empty(t, fundsRes.Halted)
	assert.NotEmpty(t, fundsRes.Stale)
}

func TestManualStopWinsAnOlderPostAddUndone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	observed, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	_, _, changed, err := h.st.stopWithCause(h.ctx, sprint.DoneCause, observed.RunSeq)
	require.NoError(t, err)
	require.True(t, changed)
	b := &pausedTakeAcquire{Mem: h.m, verb: "machine undone lock", entered: make(chan struct{}), release: make(chan struct{})}
	h.st.B = b
	finished := make(chan error, 1)
	go func() { finished <- h.st.undone(h.ctx) }()
	<-b.entered // post-add cleanup saw DONE but has not acquired the fence
	_, _, _, err = h.st.StopUntil(h.ctx, "operator cancellation", h.now.Add(time.Hour))
	require.NoError(t, err)
	close(b.release)
	require.NoError(t, <-finished)
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "operator cancellation", m.Reason)
	assert.Empty(t, m.Cause)
}

func TestStopReturnKeepsOwnedWorkReadyAndFencesOldFinish(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	pr := s.Work.Card("s1-1")
	wc := s.Fleet.Card(pr.F("work"))
	require.NotNil(t, wc)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	old := wc.Int("gen")
	row, branch := wc.Row, wc.F("branch")
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopped children", h.now.Add(time.Hour))
	require.NoError(t, err)
	assert.NotEmpty(t, h.run(TakeStep(sprint.TakeReq{As: row})).Refused, "STOP fences new takes")
	assert.NotEmpty(t, h.run(FinishStep(sprint.FinishReq{As: row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: old}})).Refused, "STOP fences a report before cancellation is acknowledged")
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, row+":"+wc.ID+"@1", "start identifies the owned job that has not stopped")
	r := sprint.StopReturnReq{As: row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: old}, Reason: "owned child stopped"}
	h.must(StopReturnStep(r))
	back := h.snap().Fleet.Card(wc.ID)
	require.NotNil(t, back)
	assert.Equal(t, row, back.Row)
	assert.Equal(t, sprint.Ready, back.Col)
	assert.Equal(t, old+1, back.Int("gen"))
	assert.Equal(t, branch, back.F("branch"))
	assert.Equal(t, wc.ID, h.snap().Work.Card(pr.ID).F("work"), "same attempt and branch resume")
	assert.Empty(t, h.must(StopReturnStep(r)).Moved, "ack replay is idempotent")
	h.startMachine()
	late := h.run(FinishStep(sprint.FinishReq{As: row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: old}, Head: "0123456789012345678901234567890123456789"}))
	assert.NotEmpty(t, late.Refused, "old child cannot finish the returned card")
	h.clean("returned work")
}

func TestStopReturnReasksSameReadAndFencesLateVerdict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rc := h.snap().Readers.Of("s1-1")[0]
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	old := max(rc.Int("gen"), 1)
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopped children", h.now.Add(time.Hour))
	require.NoError(t, err)
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}})).Refused)
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}})).Refused, "STOP fences a late read before cancellation is acknowledged")
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, rc.Row+":"+rc.ID+"@1", "start identifies the reader that has not stopped")
	h.must(StopReturnStep(sprint.StopReturnReq{As: rc.Row, IDs: []string{rc.ID}, Gens: map[string]int{rc.ID: old}, Reason: "reader child stopped"}))
	back := h.snap().Readers.Card(rc.ID)
	require.NotNil(t, back)
	assert.Equal(t, sprint.Asked, back.Col)
	assert.Equal(t, rc.Row, back.Row)
	assert.Equal(t, old+1, back.Int("gen"))
	late := h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: old}}))
	assert.NotEmpty(t, late.Refused, "old reader cannot report after STOP")
	assert.Equal(t, sprint.Asked, h.snap().Readers.Card(rc.ID).Col)
	h.startMachine()
	staleBegin := h.run(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: old}}))
	assert.NotEmpty(t, staleBegin.Refused, "the cached Asked g1 packet cannot begin returned Asked g2")
	assert.Equal(t, sprint.Asked, h.snap().Readers.Card(rc.ID).Col)
	bareBegin := h.run(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	assert.NotEmpty(t, bareBegin.Refused, "a named returned read must carry its current generation")
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: old + 1}}))
	assert.Equal(t, sprint.Reading, h.snap().Readers.Card(rc.ID).Col, "the returned read can begin from its same-owner queue")
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}})).Refused, "a post-STOP verdict must name its generation")
	h.clean("returned read")
}

func TestFreshReadSelectionCanBeginReturnedAskedGeneration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rc := h.snap().Readers.Of("s1-1")[0]
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: max(rc.Int("gen"), 1)}}))
	old := max(rc.Int("gen"), 1)
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopped child", h.now.Add(time.Hour))
	require.NoError(t, err)
	h.must(StopReturnStep(sprint.StopReturnReq{As: rc.Row, IDs: []string{rc.ID}, Gens: map[string]int{rc.ID: old}, Reason: "child exited"}))
	h.startMachine()
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{Limit: 1}}))
	assert.Equal(t, sprint.Reading, h.snap().Readers.Card(rc.ID).Col)
}

func TestReadVerdictRejectsWrongExplicitGenerationBeforeStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rc := h.snap().Readers.Of("s1-1")[0]
	gen := max(rc.Int("gen"), 1)
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: gen}}))
	wrong := h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: gen + 1}}))
	require.NotEmpty(t, wrong.Refused)
	assert.Contains(t, wrong.Refused[0].Why, "stale: generation")
	assert.Equal(t, sprint.Reading, h.snap().Readers.Card(rc.ID).Col)
}

func TestAutomaticFundsStopRetainsDebtUntilOwnerReturns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	_, stopped, changed, err := h.st.stopWithCause(h.ctx, sprint.FundsCause, m.RunSeq)
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, stopped.StopDebt, 1)
	assert.Equal(t, StopLease{Table: sprint.Fleet, Row: wc.Row, ID: wc.ID, Gen: gen}, stopped.StopDebt[0])
	assert.NotEmpty(t, h.run(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}})).Refused)
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, wc.Row+":"+wc.ID+"@1")
	h.must(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "child exited"}))
	_, running, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	assert.True(t, running.Running())
	assert.Empty(t, running.StopDebt)
}

func TestFleetDownCannotRedealAStopOwnedLease(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopping child", h.now.Add(time.Hour))
	require.NoError(t, err)
	down := h.run(FleetStep(sprint.FleetReq{Op: "down", Member: wc.Row}))
	require.NotEmpty(t, down.Refused, "fleet down must not redeal an unreturned owner lease")
	assert.Contains(t, down.Refused[0].Why, "stop-return --as "+wc.Row)
	still := h.snap().Fleet.Card(wc.ID)
	require.NotNil(t, still)
	assert.Equal(t, sprint.Working, still.Col)
	assert.Equal(t, wc.Row, still.Row)
	assert.Equal(t, gen, still.Int("gen"))
	h.must(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "child exited"}))
	h.startMachine()
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: wc.Row}))
}

func TestDropMembersCannotDeleteAStopOwnedRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopping child", h.now.Add(time.Hour))
	require.NoError(t, err)
	// A cut-short sync can leave its control card off the table while the
	// captured work card remains on the row. Row deletion must preserve it.
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: sprint.CtlID(wc.Row), Remove: true})
	_, err = h.st.DropMembers(h.ctx, nil)
	require.ErrorContains(t, err, "STOP-owned "+wc.ID)
	still := h.snap().Fleet.Card(wc.ID)
	require.NotNil(t, still)
	assert.Equal(t, wc.Row, still.Row)
	assert.Equal(t, sprint.Working, still.Col)
	h.must(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "child exited"}))
	h.startMachine()
}

func TestLegacyStoppedRunProtectsItsLiveOwnerLease(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, stopped, _, err := h.st.StopUntil(h.ctx, "owner stopping child", h.now.Add(time.Hour))
	require.NoError(t, err)
	stopped.StopIssued = false // persisted by the prior binary without debt
	stopped.StopDebt = nil
	require.NoError(t, h.st.putMachine(h.ctx, stopped))
	down := h.run(FleetStep(sprint.FleetReq{Op: "down", Member: wc.Row}))
	require.NotEmpty(t, down.Refused)
	assert.Contains(t, down.Refused[0].Why, "stop-return --as "+wc.Row)
	assert.Equal(t, sprint.Working, h.snap().Fleet.Card(wc.ID).Col)
	h.must(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "child exited"}))
	h.startMachine()
}

func TestRemovedStopDebtStillRefusesRestartAndStaleReturn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, stopped, _, err := h.st.StopUntil(h.ctx, "owner stopped child", h.now.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, stopped.StopDebt, 1)
	_, again, _, err := h.st.StopUntil(h.ctx, "still stopping", h.now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, stopped.StopDebt, again.StopDebt, "a repeated STOP preserves unresolved debt")
	assert.NotEmpty(t, h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "operator removed primary"})).Refused,
		"a native drop cannot remove a captured owner lease")
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: wc.ID, Remove: true}) // out-of-band corruption still cannot erase debt
	assert.NotEmpty(t, h.run(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "stale child"})).Refused)
	reopened := *h.st // another coordinator instance reads the persisted machine debt
	_, _, _, err = reopened.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, wc.Row+":"+wc.ID+"@1", "removing the card cannot erase the recorded cancellation debt")
}

func TestDoneStopWithoutActiveChildrenHasNoDebt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	_, stopped, changed, err := h.st.stopWithCause(h.ctx, sprint.DoneCause, m.RunSeq)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Empty(t, stopped.StopDebt)
	_, running, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	assert.True(t, running.Running())
}

func TestMovedReturnedCardDoesNotSettleStopDebt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopped child", h.now.Add(time.Hour))
	require.NoError(t, err)
	h.must(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "child exited"}))
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-fleet", []string{"other-owner"}))
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: wc.ID,
		Move: &ntable.MemberMoveOp{Row: "other-owner", Col: sprint.Ready},
		Set:  map[string]string{"gen": fmt.Sprint(gen + 1)}})
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, wc.Row+":"+wc.ID+"@1", "a moved return receipt belongs to the original owner")
}

func TestClearCannotEraseUnreturnedStopDebt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, err := h.st.Clear(h.ctx)
	require.ErrorContains(t, err, "captured owner work/read leases")
	assert.Zero(t, h.snap().Epoch, "clear must not advance past an unreturned owner lease")
	h.must(StopReturnStep(sprint.StopReturnReq{As: wc.Row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: gen}, Reason: "child exited"}))
	h.startMachine()
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), h.snap().Epoch)
}

func TestRepeatedStopCapturesLegacyActiveLeaseOnlyOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	gen := max(wc.Int("gen"), 1)
	// Initial setup has no run generation; a legacy active lease can still
	// exist there. The first explicit STOP must make it durable.
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}}))
	_, _, _, err := h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, wc.Row+":"+wc.ID+"@1", "legacy active setup work cannot cross START without a receipt")
	_, stopped, _, err := h.st.StopUntil(h.ctx, "legacy child", h.now.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, stopped.StopDebt, 1)
	assert.NotEmpty(t, h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "removed after stop"})).Refused)
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: wc.ID, Remove: true})
	_, again, _, err := h.st.StopUntil(h.ctx, "again", h.now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, stopped.StopDebt, again.StopDebt)
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, wc.Row+":"+wc.ID+"@1")
}

func TestExplicitStopBeforeFirstStartRevokesNewTake(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	wc := h.snap().Fleet.Card(h.snap().Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	_, stopped, _, err := h.st.SetMachine(h.ctx, false)
	require.NoError(t, err)
	assert.True(t, stopped.StopIssued)
	assert.NotEmpty(t, h.run(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: max(wc.Int("gen"), 1)}})).Refused)
}

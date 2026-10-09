package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPausePreservesClaimsSettlesReportsAndUnpauseIsNotStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.startMachine()
	h.machine()
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	before, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	claims := h.snap().Fleet.Cards()
	_, paused, _, err := h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	assert.True(t, paused.Running())
	assert.False(t, paused.Admitting())
	assert.Equal(t, Paused, paused.StateWord())
	assert.Equal(t, before.RunSeq, paused.RunSeq)
	assert.Equal(t, before.Since, paused.Since)
	assert.Equal(t, before.StopDebt, paused.StopDebt)
	assert.Equal(t, claims, h.snap().Fleet.Cards(), "pause only writes the machine record")
	_, again, res, err := h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	assert.Empty(t, res.Moved)
	assert.Equal(t, paused, again)
	_, started, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	assert.True(t, started.Paused, "start cannot silently unpause")
	assert.NotEmpty(t, h.run(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{Limit: 100}})).Refused)
	assert.NotEmpty(t, h.run(AskStep(sprint.AskReq{})).Refused)
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: 1}}))
	assert.Equal(t, sprint.Review, h.snap().Work.Card("s1-1").Col, "in-flight work settles while paused")
	h.must(AddStep(sprint.AddReq{Stream: "new", IDs: []string{"new-1"}}))
	h.machine()
	assert.Equal(t, sprint.Ready, h.snap().Work.Card("new-1").Col, "added ready work remains unassigned after pause")
	assert.Empty(t, h.snap().Readers.Of("s1-1"))
	_, resumed, _, err := h.st.SetPaused(h.ctx, false)
	require.NoError(t, err)
	assert.True(t, resumed.Admitting())
	assert.Equal(t, before.RunSeq, resumed.RunSeq)
	_, _, res, err = h.st.SetPaused(h.ctx, false)
	require.NoError(t, err)
	assert.Empty(t, res.Moved)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	rc := h.snap().Readers.Of("s1-1")[0]
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	_, _, _, err = h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	other := h.snap().Readers.Of("s1-1")[1]
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: other.Row, Begin: true, Sel: sprint.Sel{IDs: []string{other.ID}}})).Refused)
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	assert.Equal(t, sprint.OK, h.snap().Readers.Card(rc.ID).Col, "in-flight read settles while paused")
	h.stopMachine()
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.False(t, m.Paused)
	_, _, _, err = h.st.SetPaused(h.ctx, false)
	assert.ErrorContains(t, err, "cannot start")
	m2, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, m, m2, "unpause cannot restart STOPPED")
}

func TestPauseFencesATakeThatReadRunningBeforePause(t *testing.T) {
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
	<-b.entered
	_, _, _, err := h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	close(b.release)
	got := <-finished
	require.NoError(t, got.err)
	require.NotEmpty(t, got.res.Refused)
	assert.Contains(t, got.res.Refused[0].Why, "PAUSED")
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(wc.ID).Col)
}

func TestPausedFriendFinishDoesNotPromoteItsQueuedReservation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Class: "flash"}})
	require.NoError(t, err)
	h.up("amy")
	brief := "c: work\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy\n\nThe task."
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: brief}, {ID: "s1-2", Brief: brief}}}))
	h.startMachine()
	h.machine()
	h.start("amy", 1)
	row := sprint.FriendRow("amy")
	active := h.snap().Fleet.Cell(row, sprint.Working)[0]
	ready := h.snap().Fleet.Cell(row, sprint.Ready)[0]
	_, _, _, err = h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	h.must(FinishStep(sprint.FinishReq{As: row, Sel: sprint.Sel{IDs: []string{active.ID}}, Gens: map[string]int{active.ID: active.Int("gen")}}))
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(ready.ID).Col)
	assert.Equal(t, ready.Int("gen"), h.snap().Fleet.Card(ready.ID).Int("gen"))
	assert.Empty(t, h.snap().Fleet.Cell(row, sprint.Working), "completion cannot refill while paused")
}

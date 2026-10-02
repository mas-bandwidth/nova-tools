package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint done stops its machine (errata 3 amendment 6; sprint.TickDone):
// the tick that finds nothing open says so once, a happened note addressed to
// the coordinator, pushes it down the coordinator's goal route, and stops the
// machine with the cause done; work added after leaves it STOPPED; a start
// runs it again, and it stops again when that work has landed.

// landThrough drives the ids through to merged and landed in their stream.
func (h *harness) landThrough(stream string, ids ...string) {
	h.t.Helper()
	h.through(ids...)
	h.must(MergeStep(sprint.MergeReq{Stream: stream, Batch: len(ids)}))
}

// machineRecord is the machine's state record.
func (h *harness) machineRecord() Machine {
	h.t.Helper()
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(h.t, err)
	return m
}

// viewState is the state text of the sprint's stored view.
func (h *harness) viewState() string {
	h.t.Helper()
	v, ok := h.m.View(h.st.Names.View())
	require.True(h.t, ok, "no view")
	return v.State
}

func TestADoneSprintStopsItsMachineAndSaysSoToTheCoordinator(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route := filepath.Join(t.TempDir(), "goal.txt")
	text := "land the sprint"
	_, _, err := h.st.SetGoal(h.ctx, h.st.Actor, &text, "file:"+route)
	require.NoError(t, err)
	h.setup(3)
	h.startMachine()
	h.tick(2 * time.Hour)
	h.landThrough("s1", "s1-1", "s1-2", "s1-3")
	m := h.machineRecord()
	if !m.Running() || h.written(sprint.NSprintDone) != 0 {
		require.Fail(t, fmt.Sprintf("the merge step stopped the machine or said the sprint done: %+v, %d", m, h.written(sprint.NSprintDone)))
	}
	res := h.machine()
	const what = "3 landed, 0 dropped, took 2h0m0s from the first start"
	if res.State != Stopped || res.Done != what || res.Hint != sprint.DoneHint {
		require.Fail(t, fmt.Sprintf("the done tick: %+v", res))
	}
	m = h.machineRecord()
	if m.Running() || m.Cause != sprint.DoneCause || !m.Done() || h.viewState() != DoneState || h.st.MachineLine(h.ctx) != "machine: DONE" {
		require.Fail(t, fmt.Sprintf("the machine after done: %+v, view %q, line %q", m, h.viewState(), h.st.MachineLine(h.ctx)))
	}
	if n := len(m.Spans); n == 0 || !m.Spans[n-1].To.IsZero() || !m.Spans[n-1].From.Equal(h.now) {
		require.Fail(t, fmt.Sprintf("no STOPPED span opened at the done: %+v", m.Spans))
	}
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(t, err)
	var done []sprint.Note
	for _, n := range notes {
		if n.Type == sprint.NSprintDone {
			done = append(done, n)
		}
	}
	if len(done) != 1 || done[0].Kind != sprint.Happened || done[0].To != h.st.Actor || done[0].What != what || done[0].Hint != sprint.DoneHint {
		require.Fail(t, fmt.Sprintf("the notes stream: %+v", done))
	}
	open := h.openOf(sprint.NSprintDone)
	require.Empty(t, open, "the sprint done opened a judgment: %+v", open)
	v, err := h.st.Inbox(h.ctx, time.Minute, time.Hour, 10000)
	require.NoError(t, err)
	if len(v.Groups) == 0 || v.Groups[0].Type != sprint.NSprintDone || v.Groups[0].To != h.st.Actor || v.Groups[0].What != what {
		require.Fail(t, fmt.Sprintf("the inbox does not show the sprint done first: %+v", v.Groups))
	}
	b, err := os.ReadFile(route)
	require.NoError(t, err, "the goal route: %q %v", b, err)
	require.Contains(t, string(b), "the sprint is done: "+what+"\n"+sprint.DoneHint, "the goal route: %q %v", b, err)
	// STOPPED: the next ticks say nothing more.
	h.tick(time.Minute)
	if res := h.machine(); res.Done != "" || h.written(sprint.NSprintDone) != 1 {
		require.Fail(t, fmt.Sprintf("a tick after the done: %+v, written %d", res, h.written(sprint.NSprintDone)))
	}

	// Work added: STOPPED still, and no longer done.
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	if m := h.machineRecord(); m.Running() || m.Cause != "" || h.viewState() != Stopped || h.st.MachineLine(h.ctx) != "machine: STOPPED" {
		require.Fail(t, fmt.Sprintf("after an add: %+v, view %q", m, h.viewState()))
	}
	if res := h.machine(); res.State != Stopped || len(res.Parts) != 0 {
		require.Fail(t, fmt.Sprintf("a tick of the stopped machine moved: %+v", res))
	}
	// Started: it lands the card and stops again.
	h.startMachine()
	h.tick(time.Hour)
	h.landThrough("s1", "s1-4")
	res = h.machine()
	if res.Done != "4 landed, 0 dropped, took 3h1m0s from the first start" || !h.machineRecord().Done() || h.written(sprint.NSprintDone) != 2 {
		require.Fail(t, fmt.Sprintf("the second done: %+v, written %d", res, h.written(sprint.NSprintDone)))
	}
	// A stop by hand of a done machine: STOPPED, not done, no note.
	h.stopMachine()
	if m := h.machineRecord(); m.Running() || m.Cause != "" || h.viewState() != Stopped || h.written(sprint.NMachineStopped) != 0 {
		require.Fail(t, fmt.Sprintf("a stop by hand after the done: %+v, view %q, stop notes %d", m, h.viewState(), h.written(sprint.NMachineStopped)))
	}
	h.clean("done twice")
}

// A start of a done sprint with no work added: the first tick finds it done,
// says so and stops again.
func TestAStartOfADoneSprintStopsAgainAtTheFirstTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.landThrough("s1", "s1-1")
	res := h.machine()
	require.NotEmpty(t, res.Done, "not done: %+v", res)
	h.tick(time.Second)
	h.startMachine()
	require.True(t, h.machineRecord().Running(), "start did not start")
	if res := h.machine(); res.Done != "1 landed, 0 dropped, took 1s from the first start" || !h.machineRecord().Done() {
		require.Fail(t, fmt.Sprintf("a start of a done sprint: %+v", res))
	}
}

// FirstStart is the end of the first STOPPED span after the epoch began.
func TestTheFirstStartIsTheEndOfTheFirstSpanAfterTheEpochBegan(t *testing.T) {
	t.Parallel()
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	m := Machine{Spans: []Span{{From: at(0), To: at(1)}, {From: at(2), To: at(3)}, {From: at(4), To: at(6)}, {From: at(7)}}}
	for _, c := range []struct {
		after time.Time
		want  time.Time
	}{{time.Time{}, at(1)}, {at(1), at(3)}, {at(5), at(6)}, {at(6), time.Time{}}} {
		got := m.FirstStart(c.after)
		assert.True(t, got.Equal(c.want), "FirstStart(%v) = %v, want %v", c.after, got, c.want)
	}
}

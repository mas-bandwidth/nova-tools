package sprint

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const windowBin = "/Users/seat/.local/bin/nova-sprint"

// fakeWindow is a process table that changes on each read, and a clock that
// moves only when the window sleeps: no real time.
type fakeWindow struct {
	reads  [][]WindowProc // the table on each read; the last repeats
	n      int
	now    time.Time
	slept  []time.Duration
	failAt int // the read that fails, 0 for none
}

func (f *fakeWindow) procs(context.Context) ([]WindowProc, error) {
	f.n++
	if f.failAt == f.n {
		return nil, errors.New("ps: exited 1")
	}
	return f.reads[min(f.n, len(f.reads))-1], nil
}

func (f *fakeWindow) window(stopped []WindowAgent, bound time.Duration) AdoptWindow {
	return AdoptWindow{
		Stopped: stopped, Binary: windowBin, Bound: bound, Poll: time.Second,
		Procs: f.procs,
		Now:   func() time.Time { return f.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			f.slept = append(f.slept, d)
			f.now = f.now.Add(d)
			return nil
		},
	}
}

var (
	serverAgent = WindowAgent{Label: "com.nova.sprint.server", PID: 4100}
	memberAgent = WindowAgent{Label: "com.nova.swarm.member", PID: 4200}
	serverProc  = WindowProc{PID: 4100, Path: windowBin, Args: windowBin + " run --listen 127.0.0.1:7070"}
	memberProc  = WindowProc{PID: 4200, Path: "/Users/seat/.local/bin/nova-swarm", Args: "nova-swarm member --as m1"}
	// the live dashboard's one-second poll: a link to the seat's binary
	dashboardPoll = WindowProc{PID: 21128, Path: windowBin, Args: "/Users/seat/dashboard/bin/nova-sprint-int2 where --json"}
	inboxWait     = WindowProc{PID: 9001, Path: windowBin, Args: "nova-sprint inbox --wait"}
	shell         = WindowProc{PID: 77, Path: "/bin/zsh", Args: "-zsh"}
)

func TestAdoptWindowRefusesAStoppedAgentStillRunningPastTheBound(t *testing.T) {
	t.Parallel()
	f := &fakeWindow{now: time.Unix(1_800_000_000, 0), reads: [][]WindowProc{
		{serverProc, memberProc, dashboardPoll, shell},
		{memberProc, dashboardPoll, shell}, // the server is gone; the member still drains
	}}
	r, err := f.window([]WindowAgent{serverAgent, memberAgent}, 10*time.Second).Wait(t.Context())
	require.NoError(t, err)
	assert.False(t, r.OK)
	assert.Equal(t, []WindowAgent{memberAgent}, r.Running)
	assert.Equal(t, 10*time.Second, r.Waited)
	require.NotEmpty(t, r.Lines)
	last := r.Lines[len(r.Lines)-1]
	assert.Equal(t, "WINDOW REFUSED after 10s: these stopped agents still run: 4200 com.nova.swarm.member", last)
	// the refusal names the stopped agent and nothing else: never the dashboard's poll
	assert.NotContains(t, last, "21128")
	assert.NotContains(t, last, "where")
	// the clock moved only by the window's own sleeps, each at most the poll
	for _, d := range f.slept {
		assert.LessOrEqual(t, d, time.Second)
	}
}

func TestAdoptWindowListsAVerbProcessOfTheBinaryAsOtherAndDoesNotRefuse(t *testing.T) {
	t.Parallel()
	// the dashboard's poll and the seat's inbox --wait run the binary for the
	// whole window; the stopped server is gone on the second read
	f := &fakeWindow{now: time.Unix(1_800_000_000, 0), reads: [][]WindowProc{
		{serverProc, dashboardPoll, inboxWait, shell},
		{dashboardPoll, inboxWait, shell},
	}}
	r, err := f.window([]WindowAgent{serverAgent}, DefaultAdoptWindow).Wait(t.Context())
	require.NoError(t, err)
	assert.True(t, r.OK)
	assert.Empty(t, r.Running)
	assert.Equal(t, []WindowProc{inboxWait, dashboardPoll}, r.Others)
	assert.Equal(t, []string{
		"OTHER 9001 nova-sprint inbox --wait",
		"OTHER 21128 /Users/seat/dashboard/bin/nova-sprint-int2 where --json",
		"WINDOW OK stopped=1 waited=1s others=2",
	}, r.Lines)
}

func TestAdoptWindowSaysOKWithTheCountsWhenEveryStoppedAgentIsGone(t *testing.T) {
	t.Parallel()
	f := &fakeWindow{now: time.Unix(1_800_000_000, 0), reads: [][]WindowProc{
		{serverProc, memberProc, dashboardPoll},
		{memberProc, dashboardPoll},
		{memberProc},
		{shell},
	}}
	stopped := []WindowAgent{serverAgent, memberAgent, {Label: "com.nova.disk-guard", PID: 0}}
	r, err := f.window(stopped, DefaultAdoptWindow).Wait(t.Context())
	require.NoError(t, err)
	assert.True(t, r.OK)
	assert.Equal(t, 3*time.Second, r.Waited)
	assert.Equal(t, []string{"WINDOW OK stopped=3 waited=3s others=0"}, r.Lines)
	assert.Equal(t, 4, f.n, "the table is read until the last stopped pid is gone, and no more")
}

func TestAdoptWindowWithNothingRunningClosesOnTheFirstRead(t *testing.T) {
	t.Parallel()
	f := &fakeWindow{now: time.Unix(1_800_000_000, 0), reads: [][]WindowProc{{dashboardPoll, shell}}}
	r, err := f.window([]WindowAgent{serverAgent}, DefaultAdoptWindow).Wait(t.Context())
	require.NoError(t, err)
	assert.True(t, r.OK)
	assert.Empty(t, f.slept)
	assert.Equal(t, "WINDOW OK stopped=1 waited=0s others=1", r.Lines[len(r.Lines)-1])
}

func TestAdoptWindowATableThatDoesNotReadIsAnError(t *testing.T) {
	t.Parallel()
	f := &fakeWindow{now: time.Unix(1_800_000_000, 0), failAt: 2, reads: [][]WindowProc{{serverProc}}}
	_, err := f.window([]WindowAgent{serverAgent}, DefaultAdoptWindow).Wait(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the process table does not read")
}

func TestAdoptWindowDefaults(t *testing.T) {
	t.Parallel()
	bound, poll := AdoptWindow{}.bounds()
	assert.Equal(t, 60*time.Second, bound)
	assert.Equal(t, time.Second, poll)
	_, err := AdoptWindow{}.Wait(t.Context())
	require.Error(t, err)
}

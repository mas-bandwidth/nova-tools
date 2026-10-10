package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// TestWindowProcsOfResolvesTheDashboardLinkToTheBinary: ps's text as the
// window's table, the dashboard's nova-sprint-int2 link resolved to the
// installed nova-sprint it names, and this process left out.
func TestWindowProcsOfResolvesTheDashboardLinkToTheBinary(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the dashboard link is a symlink; the seat is macOS")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	dash := filepath.Join(home, "dashboard", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.MkdirAll(dash, 0o755))
	sprintBin := filepath.Join(bin, "nova-sprint")
	require.NoError(t, os.WriteFile(sprintBin, []byte("x"), 0o755))
	link := filepath.Join(dash, "nova-sprint-int2")
	require.NoError(t, os.Symlink(sprintBin, link))
	want := resolvedExe(sprintBin)

	text := strings.Join([]string{
		"  4100 " + sprintBin + " run --listen 127.0.0.1:7070",
		" 21128 " + link + " where --json",
		"   500 " + sprintBin + " adopt window",
		"garbage",
	}, "\n")
	procs := windowProcsOf(text, 500)
	require.Len(t, procs, 2)
	assert.Equal(t, 4100, procs[0].PID)
	assert.Equal(t, want, procs[0].Path)
	assert.Equal(t, 21128, procs[1].PID)
	assert.Equal(t, want, procs[1].Path, "the link resolves to the binary it names")
	assert.Equal(t, link+" where --json", procs[1].Args)
}

func TestAdoptWindowVerbWaitsOnlyOnTheStoppedAgents(t *testing.T) {
	t.Parallel()
	bin := "/seat/.local/bin/nova-sprint"
	reads := [][]WindowProc{
		{{PID: 4100, Path: bin, Args: bin + " run"}, {PID: 21128, Path: bin, Args: "nova-sprint-int2 where --json"}},
		{{PID: 21128, Path: bin, Args: "nova-sprint-int2 where --json"}},
	}
	n := 0
	a := newApp(func(string) string { return "" })
	windowProcsFor.Store(a, func(context.Context) ([]WindowProc, error) {
		n++
		return reads[min(n, len(reads))-1], nil
	})
	t.Cleanup(func() { windowProcsFor.Delete(a) })
	var out, errs bytes.Buffer
	code := a.cmdAdoptWindow([]string{"--binary", bin, "--stopped", "com.nova.sprint.server=4100", "--stopped", "com.nova.loop.guard=0"}, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Equal(t, "OTHER 21128 nova-sprint-int2 where --json\nWINDOW OK stopped=2 waited=1s others=1\n", out.String())
}

func TestAdoptWindowVerbRefusesAStoppedAgentPastTheBound(t *testing.T) {
	t.Parallel()
	bin := "/seat/.local/bin/nova-sprint"
	a := newApp(func(string) string { return "" })
	windowProcsFor.Store(a, func(context.Context) ([]WindowProc, error) {
		return []WindowProc{{PID: 4200, Path: "/seat/.local/bin/nova-swarm", Args: "nova-swarm member"}}, nil
	})
	t.Cleanup(func() { windowProcsFor.Delete(a) })
	var out, errs bytes.Buffer
	code := a.cmdAdoptWindow([]string{"--binary", bin, "--window", "5s", "--stopped", "com.nova.loop.member=4200"}, &out, &errs)
	require.Equal(t, 1, code)
	assert.Equal(t, "WINDOW REFUSED after 5s: these stopped agents still run: 4200 com.nova.loop.member\n", out.String())
}

func TestAdoptWindowVerbRefusesABadStopped(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	for _, args := range [][]string{
		{"--stopped", "com.nova.x"},
		{"--stopped", "=12"},
		{"--stopped", "com.nova.x=-1"},
		{"--window", "0s"},
		{"a-word"},
	} {
		var out, errs bytes.Buffer
		code := a.cmdAdoptWindow(args, &out, &errs)
		assert.Equal(t, 2, code, "%v: %s", args, errs.String())
		assert.Empty(t, out.String())
	}
}

// The live seat play passes each stopped label and pid as a flag/value pair,
// the argument shape cmdAdoptWindow accepts.
func TestSeatPlayPassesStoppedAgentsAsAdoptWindowFlags(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "fleet", "tools.yml"))
	require.NoError(t, err)
	play := string(raw)
	fact := regexp.MustCompile(`(?m)^\s*seat_window_flags:.*$`).FindString(play)
	require.NotEmpty(t, fact, "the live seat play must build its stopped-agent argv")
	require.Regexp(t, regexp.MustCompile(`\+ \['--stopped', item\.label ~ '='`), fact,
		"the live seat play must build a --stopped flag before each label=pid")
	window := regexp.MustCompile(`(?s)- name: "window: only those agents are waited on.*?register: seat_quiet`).FindString(play)
	require.Contains(t, window, "'--window', nova_seat_quiet ~ 's'] + (seat_window_flags | default([]))",
		"the adopt window argv must append the stopped flag/value pairs")
}

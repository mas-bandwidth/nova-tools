package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	reads := [][]sprint.WindowProc{
		{{PID: 4100, Path: bin, Args: bin + " run"}, {PID: 21128, Path: bin, Args: "nova-sprint-int2 where --json"}},
		{{PID: 21128, Path: bin, Args: "nova-sprint-int2 where --json"}},
	}
	n := 0
	a := newApp(func(string) string { return "" })
	windowProcsFor.Store(a, func(context.Context) ([]sprint.WindowProc, error) {
		n++
		return reads[min(n, len(reads))-1], nil
	})
	t.Cleanup(func() { windowProcsFor.Delete(a) })
	var out, errs bytes.Buffer
	code := a.cmdAdoptWindow([]string{"--binary", bin, "--stopped", "com.nova.sprint.server=4100", "--stopped", "com.nova.loop.guard=0"}, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Equal(t, "OTHER 21128 nova-sprint-int2 where --json\nWINDOW OK stopped=2 waited=1s others=1\n", out.String())
}

// TestAdoptWindowVerbAcceptsThePlaybookArgumentShape is the seat play's
// shipped argv: the candidate's list `[--binary, <bin>, --window, <N>s]` with
// each stopped agent as the pair `--stopped <label>=<pid>` (an interval agent
// between runs is pid 0). The verb reads only repeated `--stopped` flags and
// refuses positional words, so the play must put the flag before each value;
// this builds that exact list and runs it.
func TestAdoptWindowVerbAcceptsThePlaybookArgumentShape(t *testing.T) {
	t.Parallel()
	bin := "/seat/.local/bin/nova-sprint"
	stopped := []sprint.WindowAgent{
		{Label: "com.nova.sprint.server", PID: 4100},
		{Label: "com.nova.loop.member", PID: 4200},
		{Label: "com.nova.disk-guard", PID: 0},
	}
	// the playbook's seat_window_flags: each agent as --stopped <label>=<pid>
	flags := make([]string, 0, 2*len(stopped))
	for _, a := range stopped {
		flags = append(flags, "--stopped", a.Label+"="+strconv.Itoa(a.PID))
	}
	// the playbook's argv after the verb: --binary, --window, then the flags
	args := append([]string{"--binary", bin, "--window", "60s"}, flags...)

	reads := [][]sprint.WindowProc{
		{{PID: 4100, Path: bin, Args: bin + " run"}, {PID: 4200, Path: "/seat/.local/bin/nova-swarm", Args: "nova-swarm member"}, {PID: 21128, Path: bin, Args: "nova-sprint-int2 where --json"}},
		{{PID: 21128, Path: bin, Args: "nova-sprint-int2 where --json"}},
	}
	n := 0
	a := newApp(func(string) string { return "" })
	windowProcsFor.Store(a, func(context.Context) ([]sprint.WindowProc, error) {
		n++
		return reads[min(n, len(reads))-1], nil
	})
	t.Cleanup(func() { windowProcsFor.Delete(a) })
	var out, errs bytes.Buffer
	code := a.cmdAdoptWindow(args, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Equal(t, "OTHER 21128 nova-sprint-int2 where --json\nWINDOW OK stopped=3 waited=1s others=1\n", out.String())
}

func TestAdoptWindowVerbRefusesAStoppedAgentPastTheBound(t *testing.T) {
	t.Parallel()
	bin := "/seat/.local/bin/nova-sprint"
	a := newApp(func(string) string { return "" })
	windowProcsFor.Store(a, func(context.Context) ([]sprint.WindowProc, error) {
		return []sprint.WindowProc{{PID: 4200, Path: "/seat/.local/bin/nova-swarm", Args: "nova-swarm member"}}, nil
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

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
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

// TestAdoptWindowVerbTakesTheSeatPlaysWindowArgv exercises the argv the seat
// play's window step builds (fleet/tools.yml): the candidate's `adopt window`
// with --binary and --window, then one `--stopped <label>=<pid>` per agent the
// adoption stopped. The play once built seat_window_flags as bare
// `<label>=<pid>` words and appended them without `--stopped`; the verb reads
// those as positionals and refuses at exit 2 before it waits, so the shipped
// window call could not complete any adoption that stopped an agent. This
// holds the play's own set_fact to the flag and runs the shape it builds.
func TestAdoptWindowVerbTakesTheSeatPlaysWindowArgv(t *testing.T) {
	t.Parallel()
	play, err := os.ReadFile(filepath.Join("..", "..", "fleet", "tools.yml"))
	require.NoError(t, err)
	require.Contains(t, string(play), `['--stopped', item.label ~ '=' ~ (item.pid | default(0) | string)]`,
		"the seat play's seat_window_flags must carry --stopped before each bare <label>=<pid>")

	bin := "/seat/.local/bin/nova-sprint"
	a := newApp(func(string) string { return "" })
	windowProcsFor.Store(a, func(context.Context) ([]sprint.WindowProc, error) { return nil, nil })
	t.Cleanup(func() { windowProcsFor.Delete(a) })

	// the play's argv: the candidate's verb, then the stop flags it built
	argv := []string{"--binary", bin, "--window", "60s",
		"--stopped", "com.nova.loop.srv=4100",
		"--stopped", "com.nova.loop.mem=0"}
	var out, errs bytes.Buffer
	code := a.cmdAdoptWindow(argv, &out, &errs)
	require.Equal(t, 0, code, "the play's argv must be the verb, not usage: %s", errs.String())
	assert.Equal(t, "WINDOW OK stopped=2 waited=0s others=0\n", out.String())
}

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func TestStartAndStopSayTheStateBeforeAndAfter(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	out := ta.ok("where")
	require.Contains(t, out, "SPRINT TABLE\n\nSTOPPED\n\n", "where")
	require.NotContains(t, out, "machine:", "where")
	out = ta.ok("stop")
	require.Contains(t, out, "STOP OK before=STOPPED after=STOPPED unchanged: the machine is STOPPED already", "stop when stopped")
	require.Contains(t, out, "\nSTOPPED  0/3 0.0%", "stop when stopped")
	require.NotContains(t, out, "-> ETA", "stop when stopped")
	out = ta.ok("start")
	require.Contains(t, out, "START OK before=STOPPED after=RUNNING changed", "start")
	require.Contains(t, out, "nothing is ticking: run: nova-sprint run", "start")
	require.Contains(t, out, "0/3 0.0% -> ETA -  machine: running", "start")
	require.Contains(t, ta.ok("start"), "unchanged: the machine is RUNNING already", "start when running")
	out = ta.ok("tick")
	require.Contains(t, out, "MOVED deal: s1-1 work ready -> working", "tick")
	require.Contains(t, out, "TICK OK state=RUNNING idle=no moved=3", "tick")
	ta.a.sleep(store.MachineSilence + time.Second)
	out = ta.ok("inbox")
	require.Contains(t, out, "machine: STOPPED\n", "inbox with no tick")
	require.NotContains(t, out, "(no tick", "inbox with no tick")
	out = ta.ok("take --as m1 --limit 1")
	require.Contains(t, out, "STOPPED  ", "a verb's line with no tick")
	require.NotContains(t, out, "(no tick", "a verb's line with no tick")
	require.NotContains(t, out, "-> ETA", "a verb's line with no tick")
	ta.ok("stop")
	require.Contains(t, ta.ok("tick"), "TICK OK state=STOPPED nothing done", "a tick while stopped")
}

func TestRunTicksOnlyWhileRunning(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run: %d", code)
	var out, errb bytes.Buffer
	ta.a.runLoop(context.Background(), st, 20, 3, &out, &errb)
	require.NotContains(t, out.String(), "MOVED", "run while stopped:\n%s", out.String())
	require.Contains(t, out.String(), "machine STOPPED", "run while stopped:\n%s", out.String())
	ta.ok("start")
	out.Reset()
	ta.a.runLoop(context.Background(), st, 20, 3, &out, &errb)
	require.Contains(t, out.String(), "machine RUNNING", "run while running:\n%s", out.String())
	require.Contains(t, out.String(), "MOVED deal: s1-1 work ready -> working", "run while running:\n%s", out.String())
	require.Contains(t, ta.ok("where"), "SPRINT TABLE\n\n0/3 0.0% -> ETA -\n\n", "where after run")
}

// whereHead is what the where view says under its title: the lines between
// SPRINT TABLE and the first table, with the clock line before the title cut.
func whereHead(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "SPRINT TABLE\n")
	require.GreaterOrEqual(t, i, 0, "where has no title:\n%s", out)
	return out[i:]
}

func TestWhereHeaderIsStoppedOrTheProgressLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	// stopped: the title, a blank line, the word, a blank line, byte for byte
	got := whereHead(t, ta.ok("where"))
	require.True(t, strings.HasPrefix(got, "SPRINT TABLE\n\nSTOPPED\n\n"), "stopped:\n%q", got)
	require.False(t, strings.HasPrefix(got, "SPRINT TABLE\n\nSTOPPED\n\n\n"), "stopped:\n%q", got)
	// running with no cards
	ta.ok("start")
	got = whereHead(t, ta.ok("where"))
	require.True(t, strings.HasPrefix(got, "SPRINT TABLE\n\n0/0 0.0% -> ETA -\n\n"), "running with no cards:\n%q", got)
	// running with cards
	ta.ok("add --stream s1 --count 3")
	ta.ok("tick")
	out := ta.ok("where")
	got = whereHead(t, out)
	require.True(t, strings.HasPrefix(got, "SPRINT TABLE\n\n0/3 0.0% -> ETA -\n\n"), "running with cards:\n%q", out)
	require.NotContains(t, out, "machine:", "running with cards:\n%q", out)
	require.NotContains(t, out, "coordinator:", "running with cards:\n%q", out)
	// running but silent: never hidden
	ta.a.sleep(store.MachineSilence + time.Second)
	got = whereHead(t, ta.ok("where"))
	require.True(t, strings.HasPrefix(got, "SPRINT TABLE\n\nSTOPPED\n\n"), "running but silent:\n%q", got)
	require.NotContains(t, got, "(no tick", "running but silent:\n%q", got)
}

func TestWhereHeaderStoppedIsExactlyTheView(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	out := ta.ok("where")
	i := strings.Index(out, "work")
	require.GreaterOrEqual(t, i, 0, "stopped view:\n%q", out)
	require.Equal(t, "SPRINT TABLE\n\nSTOPPED\n\n", out[:i], "stopped view:\n%q", out)
}

// TestWhereDoesNotPrintTheRoutesLine pins the view going from the machine state line
// to the work table with one blank line between, on a store that holds routes: no
// `routes: flash=n pro=m` line in the text, no `routes` in the JSON (`nova-sprint
// routes` is where the tiers are read).
func TestWhereDoesNotPrintTheRoutesLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.m.SetRoutes([]sprint.Route{
		{Name: "flash-a", Tier: "flash", Provider: "p", Model: "m", Enabled: true},
		{Name: "pro-a", Tier: "pro", Provider: "p", Model: "m", Enabled: true},
	})
	out := ta.ok("where")
	require.NotContains(t, out, "routes:")
	require.Contains(t, out, "SPRINT TABLE\n\nSTOPPED\n\nwork")
	require.NotContains(t, ta.ok("where --json"), `"routes"`)
	require.Contains(t, ta.ok("routes"), "flash=1 pro=1")
}

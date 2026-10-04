package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalVerbsSetShowDropAndTheTickDelivers(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "goal.txt")
	require.NoError(t, os.WriteFile(text, []byte("keep going\nand report\n"), 0o644))
	route := "file:" + filepath.Join(dir, "reminder-a.txt")
	ta.ok("init --readers reader-a,reader-b --members m1")
	out := ta.ok("goal set friend-a --file " + text + " --to " + route)
	require.Contains(t, out, "GOAL-SET OK name=friend-a new=true route="+route, "goal set")
	require.Contains(t, out, "every 5m0s of running time", "goal set")
	out = ta.ok("goal show friend-a")
	require.Contains(t, out, "GOAL friend-a route="+route+" last=never count=0 state=waiting", "goal show: %s", out)
	require.True(t, strings.HasSuffix(out, "keep going\nand report\n"), "goal show: %s", out)
	require.Contains(t, ta.ok("tick"), "state=STOPPED", "a stopped tick")
	_, err := os.Stat(filepath.Join(dir, "reminder-a.txt"))
	require.Error(t, err, "delivered while stopped")
	ta.ok("start")
	out = ta.ok("tick")
	require.Contains(t, out, "MOVED remind: REMINDER 1 to friend-a over "+route, "tick")
	b, _ := os.ReadFile(filepath.Join(dir, "reminder-a.txt"))
	require.True(t, strings.HasPrefix(string(b), "REMINDER 1 to friend-a at "+t0.Format(time.RFC3339)+", epoch 0\nkeep going\n"), "file: %s", b)
	// the frame holds the tables and no line about the people; goal show
	// says each person's last push, and where --json carries it
	out = ta.ok("where")
	require.NotContains(t, out, "REMINDERS", "where")
	require.NotContains(t, out, "friend-a", "where")
	out = ta.ok("goal show")
	require.Contains(t, out, "friend-a route="+route+" last="+t0.Format(time.RFC3339)+" count=1 state=ok", "goal show")
	var w whereView
	ta.json("where", &w)
	require.Len(t, w.Goals, 1, "where --json: %+v", w.Goals)
	require.Equal(t, 1, w.Goals[0].Count, "where --json: %+v", w.Goals)
	require.Equal(t, "ok", w.Goals[0].State, "where --json: %+v", w.Goals)
	// The route alone changes without the text.
	ta.ok("goal set friend-a --to file:" + filepath.Join(dir, "elsewhere.txt"))
	out = ta.ok("goal show friend-a")
	require.Contains(t, out, "elsewhere.txt", "route change")
	require.Contains(t, out, "keep going", "route change")
	require.Contains(t, ta.ok("goal drop friend-a"), "GOAL-DROP OK name=friend-a dropped", "drop")
	require.Contains(t, ta.ok("goal drop friend-a"), "unchanged", "drop again")
	require.Contains(t, ta.ok("goal show"), "GOAL none", "show none")
}

func TestGoalSetRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	ok := filepath.Join(dir, "ok.txt")
	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("x", 8*1024+1)), 0o644))
	require.NoError(t, os.WriteFile(ok, []byte("goal"), 0o644))
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, line := range []string{
		"goal set friend-a --file " + big,
		"goal set friend-a --file " + filepath.Join(dir, "missing.txt"),
		"goal set friend-a --file " + ok + " --to bus:" + dir,
		"goal set friend-a --file " + ok + " --to file:relative",
		"goal set friend-a",
		"goal set Friend --file " + ok,
		"goal set --file " + ok,
		"goal show nobody",
		"goal drop",
		"goal",
	} {
		code, _, errs := ta.do(line)
		assert.NotEqual(t, 0, code, "%s: exit %d, %q", line, code, errs)
		assert.NotEmpty(t, errs, "%s: exit %d, %q", line, code, errs)
	}
	require.Contains(t, ta.ok("goal show"), "GOAL none", "a refused set stored someone")
}

func TestGoalSetDefaultRouteIsPrintedAndHelpNamesGoal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	ta.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_REMINDER_DIR" {
			return dir
		}
		return map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	}
	ok := filepath.Join(dir, "ok.txt")
	require.NoError(t, os.WriteFile(ok, []byte("goal"), 0o644))
	ta.ok("init --readers reader-a,reader-b --members m1")
	require.Contains(t, ta.ok("goal set friend-a --file "+ok), "route=file:"+filepath.Join(dir, "friend-a.txt"), "default route")
	out := ta.ok("help goal")
	require.Contains(t, out, "goal set", "help goal: %s", out)
	require.True(t, strings.Contains(out, "every 5") || strings.Contains(out, "5\nminutes"), "help goal: %s", out)
	out = ta.ok("help")
	require.Contains(t, out, "nova-sprint goal show [<name>]", "help: %s", out)
	require.Contains(t, out, "REMINDER <n> to <name>", "help: %s", out)
}

// The reminder header names the person, the time and the epoch, and no sprint:
// a store holds one.
func TestReminderHeaderNamesNoSprint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "goal.txt")
	require.NoError(t, os.WriteFile(text, []byte("keep going\n"), 0o644))
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("goal set friend-a --file " + text + " --to file:" + filepath.Join(dir, "r.txt"))
	ta.ok("start")
	ta.ok("tick")
	b, _ := os.ReadFile(filepath.Join(dir, "r.txt"))
	require.True(t, strings.HasPrefix(string(b), "REMINDER 1 to friend-a at "+t0.Format(time.RFC3339)+", epoch 0\nkeep going\n"), "file: %s", b)
}

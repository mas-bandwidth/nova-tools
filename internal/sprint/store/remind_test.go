package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goalFile is a route in the test's own directory, and the path it names.
func goalFile(t *testing.T, name string) (route, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), name+".txt")
	return "file:" + path, path
}

func (h *harness) setGoal(name, text, route string) {
	h.t.Helper()
	_, _, err := h.st.SetGoal(h.ctx, name, &text, route)
	require.NoError(h.t, err, "goal set %s: %v", name, err)
}

func (h *harness) goal(name string) sprint.Goal {
	h.t.Helper()
	g, err := h.st.Goals(h.ctx)
	require.NoError(h.t, err, "goal %s: %v", name, err)
	require.GreaterOrEqual(h.t, g.Find(name), 0, "goal %s: %v", name, err)
	return g.People[g.Find(name)]
}

// reminded is the reminders a tick delivered, as the lines it moved.
func reminded(res TickResult) []string {
	var out []string
	for _, p := range res.Parts {
		if p.Name == "remind" {
			out = append(out, p.Moved...)
		}
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestReminderPushesAtStartAndEveryFiveMinutesOfRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "keep the queue full\nand say what you did", route)
	h.startMachine()
	res := h.machine()
	got := reminded(res)
	require.Len(t, got, 1, "the first tick after start: %v", got)
	require.True(t, strings.HasPrefix(got[0], "REMINDER 1 to friend-a"), "the first tick after start: %v", got)
	want := "REMINDER 1 to friend-a at " + t0.Format(time.RFC3339) + ", epoch 0\nkeep the queue full\nand say what you did\n"
	gotFile := readFile(t, path)
	require.Equal(t, want, gotFile, "file:\n%q\nwant\n%q", gotFile, want)
	h.tick(RemindEveryMinusASecond)
	got = reminded(h.machine())
	require.Empty(t, got, "before five minutes of running: %v", got)
	h.tick(time.Second)
	got = reminded(h.machine())
	require.Len(t, got, 1, "at five minutes: %v", got)
	require.True(t, strings.HasPrefix(got[0], "REMINDER 2 to friend-a"), "at five minutes: %v", got)
	// The file is replaced, never appended.
	gotFile = readFile(t, path)
	require.Equal(t, 1, strings.Count(gotFile, "REMINDER"), "file after the second push:\n%s", gotFile)
	require.True(t, strings.HasPrefix(gotFile, "REMINDER 2 to friend-a at "+t0.Add(sprint.RemindEvery).Format(time.RFC3339)), "file after the second push:\n%s", gotFile)
	g := h.goal("friend-a")
	require.Equal(t, 2, g.Count, "record: %+v", g)
	require.True(t, g.Last.Equal(t0.Add(sprint.RemindEvery)), "record: %+v", g)
	entries, _ := os.ReadDir(filepath.Dir(path))
	require.Len(t, entries, 1, "a temporary file was left beside it: %v", entries)
}

// RemindEveryMinusASecond is one second short of the interval.
const RemindEveryMinusASecond = sprint.RemindEvery - time.Second

func TestReminderTickIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "goal", route)
	h.startMachine()
	h.machine()
	before, _ := os.Stat(path)
	g := h.goal("friend-a")
	for i := 0; i < 3; i++ {
		res := h.machine()
		require.Empty(t, reminded(res), "a second tick right after: %+v", res)
		require.Empty(t, res.Parts, "a second tick right after: %+v", res)
	}
	after, _ := os.Stat(path)
	require.True(t, after.ModTime().Equal(before.ModTime()), "a second tick changed something: %+v then %+v", g, h.goal("friend-a"))
	require.Equal(t, g, h.goal("friend-a"), "a second tick changed something: %+v then %+v", g, h.goal("friend-a"))
}

func TestReminderNothingWhileStoppedAndStoppedTimeDoesNotCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "goal", route)
	h.startMachine()
	h.machine() // push 1 at the start
	h.tick(2 * time.Minute)
	h.stopMachine()
	for i := 0; i < 3; i++ { // three hours STOPPED, a tick each hour
		h.tick(time.Hour)
		res := h.machine()
		require.Equal(t, Stopped, res.State, "a tick while stopped: %+v", res)
		require.Empty(t, res.Parts, "a tick while stopped: %+v", res)
	}
	g := h.goal("friend-a")
	require.Equal(t, 1, g.Count, "pushed while stopped: %+v", g)
	require.True(t, strings.HasPrefix(readFile(t, path), "REMINDER 1 "), "pushed while stopped: %+v", g)
	h.startMachine()
	got := reminded(h.machine())
	require.Len(t, got, 1, "the first tick after the second start: %v", got)
	require.True(t, strings.HasPrefix(got[0], "REMINDER 2 "), "the first tick after the second start: %v", got)
	// The three hours did not bring the next push forward, nor push it back.
	h.tick(RemindEveryMinusASecond)
	got = reminded(h.machine())
	require.Empty(t, got, "early after a restart: %v", got)
	h.tick(time.Second)
	got = reminded(h.machine())
	require.Len(t, got, 1, "five minutes after the restart: %v", got)
	require.True(t, strings.HasPrefix(got[0], "REMINDER 3 "), "five minutes after the restart: %v", got)
}

func TestReminderTwoPeopleDifferentTexts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ra, pa := goalFile(t, "a")
	rb, pb := goalFile(t, "b")
	h.setGoal("friend-a", "text for a", ra)
	h.setGoal("friend-b", "text for b", rb)
	h.startMachine()
	got := reminded(h.machine())
	require.Len(t, got, 2, "at start: %v", got)
	a, b := readFile(t, pa), readFile(t, pb)
	require.True(t, strings.HasSuffix(a, "\ntext for a\n"), "a: %q b: %q", a, b)
	require.Contains(t, a, " to friend-a ", "a: %q b: %q", a, b)
	require.True(t, strings.HasSuffix(b, "\ntext for b\n"), "a: %q b: %q", a, b)
	require.Contains(t, b, " to friend-b ", "a: %q b: %q", a, b)
	// One is set two minutes in: pushed at once, then five minutes after its own push.
	h.tick(2 * time.Minute)
	rc, pc := goalFile(t, "c")
	h.setGoal("friend-c", "text for c", rc)
	got = reminded(h.machine())
	require.Len(t, got, 1, "a person set while running: %v", got)
	require.Contains(t, got[0], "to friend-c", "a person set while running: %v", got)
	require.True(t, strings.HasSuffix(readFile(t, pc), "text for c\n"), "a person set while running: %v", got)
	h.tick(3 * time.Minute)
	got = reminded(h.machine())
	require.Len(t, got, 2, "the first two are due, the third is not: %v", got)
}

func TestReminderFailingRouteWritesOneJudgmentAndSuccessCloses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// A route whose directory cannot be made: a path under a regular file.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	h.setGoal("friend-a", "goal", "file:"+filepath.Join(blocker, "sub", "a.txt"))
	h.startMachine()
	res := h.machine()
	var refused int
	for _, p := range res.Parts {
		refused += len(p.Refused)
	}
	open := h.openOf(sprint.NRemindFailed)
	require.Equal(t, 1, refused, "the first failure: refused %d, open %d, notes %d", refused, len(open), res.Notes())
	require.Len(t, open, 1, "the first failure: refused %d, open %d, notes %d", refused, len(open), res.Notes())
	require.Equal(t, 1, h.written(sprint.NRemindFailed), "the first failure: refused %d, open %d, notes %d", refused, len(open), res.Notes())
	n := open[0].Note
	require.Equal(t, sprint.Judgment, n.Kind, "the judgment: %+v", n)
	require.Contains(t, n.What, "friend-a", "the judgment: %+v", n)
	require.Contains(t, n.What, "blocker", "the judgment: %+v", n)
	require.Equal(t, "goal set friend-a --to <route>|goal drop friend-a|ack", strings.Join(n.Decisions, "|"), "the judgment: %+v", n)
	// It keeps failing, tick after tick and attempt after attempt: still one.
	for i := 0; i < 3; i++ {
		h.tick(sprint.RemindEvery)
		h.machine()
		require.Equal(t, 1, h.written(sprint.NRemindFailed), "a second judgment: written %d", h.written(sprint.NRemindFailed))
	}
	open = h.openOf(sprint.NRemindFailed)
	require.Len(t, open, 1, "open judgments: %d", len(open))
	g := h.goal("friend-a")
	require.NotEmpty(t, g.Fail, "record while failing: %+v", g)
	require.Equal(t, 0, g.Count, "record while failing: %+v", g)
	require.True(t, g.Last.IsZero(), "record while failing: %+v", g)
	// A route that works: the next attempt arrives and closes the judgment.
	route, path := goalFile(t, "a")
	_, _, err := h.st.SetGoal(h.ctx, "friend-a", nil, route)
	require.NoError(t, err)
	got := reminded(h.machine())
	require.Len(t, got, 1, "after the route was changed: %v", got)
	open = h.openOf(sprint.NRemindFailed)
	require.Empty(t, open, "the judgment stayed open after a delivery: %d", len(open))
	g = h.goal("friend-a")
	require.Empty(t, g.Fail, "record after success: %+v", g)
	require.Equal(t, 1, g.Count, "record after success: %+v", g)
	require.True(t, strings.HasPrefix(readFile(t, path), "REMINDER 1 "), "record after success: %+v", g)
	// A failure again is judged again, and dropping the person closes it.
	h.setGoal("friend-a", "goal", "file:"+filepath.Join(blocker, "again.txt"))
	h.machine()
	open = h.openOf(sprint.NRemindFailed)
	require.Len(t, open, 1, "a new failure: %d", len(open))
	ok, err := h.st.DropGoal(h.ctx, "friend-a")
	require.NoError(t, err, "drop: %v %v", ok, err)
	require.True(t, ok, "drop: %v %v", ok, err)
	h.machine()
	open = h.openOf(sprint.NRemindFailed)
	require.Empty(t, open, "a dropped person's judgment stayed open: %d", len(open))
}

func TestReminderTextBoundAndRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, _ := goalFile(t, "a")
	at := func(n int) *string { s := strings.Repeat("x", n); return &s }
	_, _, err := h.st.SetGoal(h.ctx, "friend-a", at(MaxCardTextBytes), route)
	require.NoError(t, err, "text at the bound: %v", err)
	_, _, err = h.st.SetGoal(h.ctx, "friend-a", at(MaxCardTextBytes+1), route)
	require.ErrorContains(t, err, "bound", "text over the bound: %v", err)
	blank := "  \n"
	for name, c := range map[string]struct {
		name  string
		text  *string
		route string
	}{
		"blank text":   {"friend-b", &blank, route},
		"bad name":     {"Friend B", at(3), route},
		"no route":     {"friend-b", at(3), ""},
		"no text":      {"friend-b", nil, route},
		"relative":     {"friend-b", at(3), "file:relative.txt"},
		"bus":          {"friend-b", at(3), "bus:/some/bus"},
		"unknown kind": {"friend-b", at(3), "mail:someone"},
	} {
		_, _, err := h.st.SetGoal(h.ctx, c.name, c.text, c.route)
		assert.Error(t, err, "%s: not refused", name)
	}
	g, _ := h.st.Goals(h.ctx)
	require.Len(t, g.People, 1, "a refused set stored someone: %+v", g)
}

func TestReminderClearKeepsPeopleAndForgetsPushes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "goal", route)
	h.startMachine()
	h.machine()
	h.tick(time.Minute)
	require.NoError(t, h.st.ResetGoalPushes(h.ctx))
	g := h.goal("friend-a")
	require.Equal(t, "goal", g.Text, "after a reset: %+v", g)
	require.Equal(t, route, g.Route, "after a reset: %+v", g)
	require.Equal(t, 0, g.Count, "after a reset: %+v", g)
	require.True(t, g.Last.IsZero(), "after a reset: %+v", g)
	got := reminded(h.machine())
	require.Len(t, got, 1, "the first tick after a clear: %v", got)
	require.True(t, strings.HasPrefix(got[0], "REMINDER 1 "), "the first tick after a clear: %v", got)
	require.True(t, strings.HasPrefix(readFile(t, path), "REMINDER 1 to friend-a at "+t0.Add(time.Minute).Format(time.RFC3339)), "file not replaced")
}

func TestReminderRunsOnAnIdleTickToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, _ := goalFile(t, "a")
	h.setGoal("friend-a", "goal", route)
	h.startMachine()
	h.machine()
	h.tick(sprint.RemindEvery - 30*time.Second)
	h.machine() // every tick reads and plans every table (errata 3 amendment 10)
	h.tick(30 * time.Second)
	res := h.machine()
	require.True(t, res.Idle, "an idle tick that is due: idle=%v %v", res.Idle, reminded(res))
	require.Len(t, reminded(res), 1, "an idle tick that is due: idle=%v %v", res.Idle, reminded(res))
}

// clear keeps the people and their goals and resets their pushes, so the
// first tick of the new sprint pushes to everyone.
func TestClearKeepsTheGoalsAndResetsThePushes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	route, _ := goalFile(t, "a")
	h.setGoal("a", "land the sprint", route)
	h.startMachine()
	h.machine()
	g, err := h.st.Goals(h.ctx)
	require.NoError(t, err, "before the clear: %+v %v", g, err)
	require.Len(t, g.People, 1, "before the clear: %+v %v", g, err)
	require.Equal(t, 1, g.People[0].Count, "before the clear: %+v %v", g, err)
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	g, err = h.st.Goals(h.ctx)
	require.NoError(t, err, "after the clear: %+v %v", g, err)
	require.Len(t, g.People, 1, "after the clear: %+v %v", g, err)
	require.Equal(t, "land the sprint", g.People[0].Text, "after the clear: %+v %v", g, err)
	require.Equal(t, 0, g.People[0].Count, "after the clear: %+v %v", g, err)
	require.True(t, g.People[0].Last.IsZero(), "after the clear: %+v %v", g, err)
}

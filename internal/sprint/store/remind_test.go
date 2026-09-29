package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// goalFile is a route in the test's own directory, and the path it names.
func goalFile(t *testing.T, name string) (route, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), name+".txt")
	return "file:" + path, path
}

func (h *harness) setGoal(name, text, route string) {
	h.t.Helper()
	if _, _, err := h.st.SetGoal(h.ctx, name, &text, route); err != nil {
		h.t.Fatalf("goal set %s: %v", name, err)
	}
}

func (h *harness) goal(name string) sprint.Goal {
	h.t.Helper()
	g, err := h.st.Goals(h.ctx)
	if err != nil || g.Find(name) < 0 {
		h.t.Fatalf("goal %s: %v", name, err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReminderPushesAtStartAndEveryFiveMinutesOfRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "keep the queue full\nand say what you did", route)
	h.startMachine()
	res := h.machine()
	if got := reminded(res); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 1 to friend-a") {
		t.Fatalf("the first tick after start: %v", got)
	}
	want := "REMINDER 1 to friend-a at " + t0.Format(time.RFC3339) + ", sprint t-, epoch 0\nkeep the queue full\nand say what you did\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("file:\n%q\nwant\n%q", got, want)
	}
	h.tick(RemindEveryMinusASecond)
	if got := reminded(h.machine()); len(got) != 0 {
		t.Fatalf("before five minutes of running: %v", got)
	}
	h.tick(time.Second)
	if got := reminded(h.machine()); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 2 to friend-a") {
		t.Fatalf("at five minutes: %v", got)
	}
	// The file is replaced, never appended.
	if got := readFile(t, path); strings.Count(got, "REMINDER") != 1 || !strings.HasPrefix(got, "REMINDER 2 to friend-a at "+t0.Add(sprint.RemindEvery).Format(time.RFC3339)) {
		t.Fatalf("file after the second push:\n%s", got)
	}
	if g := h.goal("friend-a"); g.Count != 2 || !g.Last.Equal(t0.Add(sprint.RemindEvery)) {
		t.Fatalf("record: %+v", g)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left beside it: %v", entries)
	}
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
		if res := h.machine(); len(reminded(res)) != 0 || len(res.Parts) != 0 {
			t.Fatalf("a second tick right after: %+v", res)
		}
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) || h.goal("friend-a") != g {
		t.Fatalf("a second tick changed something: %+v then %+v", g, h.goal("friend-a"))
	}
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
		if res := h.machine(); res.State != Stopped || len(res.Parts) != 0 {
			t.Fatalf("a tick while stopped: %+v", res)
		}
	}
	if g := h.goal("friend-a"); g.Count != 1 || !strings.HasPrefix(readFile(t, path), "REMINDER 1 ") {
		t.Fatalf("pushed while stopped: %+v", g)
	}
	h.startMachine()
	if got := reminded(h.machine()); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 2 ") {
		t.Fatalf("the first tick after the second start: %v", got)
	}
	// The three hours did not bring the next push forward, nor push it back.
	h.tick(RemindEveryMinusASecond)
	if got := reminded(h.machine()); len(got) != 0 {
		t.Fatalf("early after a restart: %v", got)
	}
	h.tick(time.Second)
	if got := reminded(h.machine()); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 3 ") {
		t.Fatalf("five minutes after the restart: %v", got)
	}
}

func TestReminderTwoPeopleDifferentTexts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ra, pa := goalFile(t, "a")
	rb, pb := goalFile(t, "b")
	h.setGoal("friend-a", "text for a", ra)
	h.setGoal("friend-b", "text for b", rb)
	h.startMachine()
	if got := reminded(h.machine()); len(got) != 2 {
		t.Fatalf("at start: %v", got)
	}
	if a, b := readFile(t, pa), readFile(t, pb); !strings.HasSuffix(a, "\ntext for a\n") || !strings.Contains(a, " to friend-a ") ||
		!strings.HasSuffix(b, "\ntext for b\n") || !strings.Contains(b, " to friend-b ") {
		t.Fatalf("a: %q b: %q", a, b)
	}
	// One is set two minutes in: pushed at once, then five minutes after its own push.
	h.tick(2 * time.Minute)
	rc, pc := goalFile(t, "c")
	h.setGoal("friend-c", "text for c", rc)
	if got := reminded(h.machine()); len(got) != 1 || !strings.Contains(got[0], "to friend-c") || !strings.HasSuffix(readFile(t, pc), "text for c\n") {
		t.Fatalf("a person set while running: %v", got)
	}
	h.tick(3 * time.Minute)
	if got := reminded(h.machine()); len(got) != 2 {
		t.Fatalf("the first two are due, the third is not: %v", got)
	}
}

func TestReminderFailingRouteWritesOneJudgmentAndSuccessCloses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// A route whose directory cannot be made: a path under a regular file.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.setGoal("friend-a", "goal", "file:"+filepath.Join(blocker, "sub", "a.txt"))
	h.startMachine()
	res := h.machine()
	var refused int
	for _, p := range res.Parts {
		refused += len(p.Refused)
	}
	open := h.openOf(sprint.NRemindFailed)
	if refused != 1 || len(open) != 1 || res.Notes() != 1 {
		t.Fatalf("the first failure: refused %d, open %d, notes %d", refused, len(open), res.Notes())
	}
	n := open[0].Note
	if n.Kind != sprint.Judgment || !strings.Contains(n.What, "friend-a") || !strings.Contains(n.What, "blocker") ||
		strings.Join(n.Decisions, "|") != "goal set friend-a --to <route>|goal drop friend-a" {
		t.Fatalf("the judgment: %+v", n)
	}
	// It keeps failing, tick after tick and attempt after attempt: still one.
	for i := 0; i < 3; i++ {
		h.tick(sprint.RemindEvery)
		if res := h.machine(); res.Notes() != 0 {
			t.Fatalf("a second judgment: %+v", res)
		}
	}
	if open := h.openOf(sprint.NRemindFailed); len(open) != 1 {
		t.Fatalf("open judgments: %d", len(open))
	}
	if g := h.goal("friend-a"); g.Fail == "" || g.Count != 0 || !g.Last.IsZero() {
		t.Fatalf("record while failing: %+v", g)
	}
	// A route that works: the next attempt arrives and closes the judgment.
	route, path := goalFile(t, "a")
	if _, _, err := h.st.SetGoal(h.ctx, "friend-a", nil, route); err != nil {
		t.Fatal(err)
	}
	if got := reminded(h.machine()); len(got) != 1 {
		t.Fatalf("after the route was changed: %v", got)
	}
	if open := h.openOf(sprint.NRemindFailed); len(open) != 0 {
		t.Fatalf("the judgment stayed open after a delivery: %d", len(open))
	}
	if g := h.goal("friend-a"); g.Fail != "" || g.Count != 1 || !strings.HasPrefix(readFile(t, path), "REMINDER 1 ") {
		t.Fatalf("record after success: %+v", g)
	}
	// A failure again is judged again, and dropping the person closes it.
	h.setGoal("friend-a", "goal", "file:"+filepath.Join(blocker, "again.txt"))
	h.machine()
	if open := h.openOf(sprint.NRemindFailed); len(open) != 1 {
		t.Fatalf("a new failure: %d", len(open))
	}
	if ok, err := h.st.DropGoal(h.ctx, "friend-a"); err != nil || !ok {
		t.Fatalf("drop: %v %v", ok, err)
	}
	h.machine()
	if open := h.openOf(sprint.NRemindFailed); len(open) != 0 {
		t.Fatalf("a dropped person's judgment stayed open: %d", len(open))
	}
}

func TestReminderTextBoundAndRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, _ := goalFile(t, "a")
	at := func(n int) *string { s := strings.Repeat("x", n); return &s }
	if _, _, err := h.st.SetGoal(h.ctx, "friend-a", at(MaxCardTextBytes), route); err != nil {
		t.Fatalf("text at the bound: %v", err)
	}
	if _, _, err := h.st.SetGoal(h.ctx, "friend-a", at(MaxCardTextBytes+1), route); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("text over the bound: %v", err)
	}
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
		if _, _, err := h.st.SetGoal(h.ctx, c.name, c.text, c.route); err == nil {
			t.Errorf("%s: not refused", name)
		}
	}
	if g, _ := h.st.Goals(h.ctx); len(g.People) != 1 {
		t.Fatalf("a refused set stored someone: %+v", g)
	}
}

func TestReminderClearKeepsPeopleAndForgetsPushes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "goal", route)
	h.startMachine()
	h.machine()
	h.tick(time.Minute)
	if err := h.st.ResetGoalPushes(h.ctx); err != nil {
		t.Fatal(err)
	}
	if g := h.goal("friend-a"); g.Text != "goal" || g.Route != route || g.Count != 0 || !g.Last.IsZero() {
		t.Fatalf("after a reset: %+v", g)
	}
	if got := reminded(h.machine()); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 1 ") {
		t.Fatalf("the first tick after a clear: %v", got)
	}
	if !strings.HasPrefix(readFile(t, path), "REMINDER 1 to friend-a at "+t0.Add(time.Minute).Format(time.RFC3339)) {
		t.Fatal("file not replaced")
	}
}

func TestReminderRunsOnAnIdleTickToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	route, _ := goalFile(t, "a")
	h.setGoal("friend-a", "goal", route)
	h.startMachine()
	if res := h.machine(); res.Idle {
		t.Fatalf("the first tick is not idle")
	}
	h.tick(sprint.RemindEvery - 30*time.Second)
	if res := h.machine(); res.Idle {
		t.Fatalf("a full read is due at %s", TickFullEvery)
	}
	h.tick(30 * time.Second)
	res := h.machine()
	if !res.Idle || len(reminded(res)) != 1 {
		t.Fatalf("an idle tick that is due: idle=%v %v", res.Idle, reminded(res))
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoalVerbsSetShowDropAndTheTickDelivers(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "goal.txt")
	if err := os.WriteFile(text, []byte("keep going\nand report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	route := "file:" + filepath.Join(dir, "reminder-a.txt")
	ta.ok("init --readers reader-a,reader-b --members m1")
	out := ta.ok("goal set friend-a --file " + text + " --to " + route)
	if !strings.Contains(out, "GOAL-SET OK name=friend-a new=true route="+route) || !strings.Contains(out, "every 5m0s of running time") {
		t.Fatalf("goal set: %s", out)
	}
	out = ta.ok("goal show friend-a")
	if !strings.Contains(out, "GOAL friend-a route="+route+" last=never count=0 state=waiting") || !strings.HasSuffix(out, "keep going\nand report\n") {
		t.Fatalf("goal show: %s", out)
	}
	if out := ta.ok("tick"); !strings.Contains(out, "state=STOPPED") {
		t.Fatalf("a stopped tick: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "reminder-a.txt")); err == nil {
		t.Fatal("delivered while stopped")
	}
	ta.ok("start")
	out = ta.ok("tick")
	if !strings.Contains(out, "MOVED remind: REMINDER 1 to friend-a over "+route) {
		t.Fatalf("tick: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "reminder-a.txt")); !strings.HasPrefix(string(b), "REMINDER 1 to friend-a at "+t0.Format(time.RFC3339)+", sprint t-sprint, epoch 0\nkeep going\n") {
		t.Fatalf("file: %s", b)
	}
	// the frame holds the tables and no line about the people; goal show
	// says each person's last push, and where --json carries it
	if out = ta.ok("where"); strings.Contains(out, "REMINDERS") || strings.Contains(out, "friend-a") {
		t.Fatalf("where: %s", out)
	}
	if out = ta.ok("goal show"); !strings.Contains(out, "friend-a route="+route+" last="+t0.Format(time.RFC3339)+" count=1 state=ok") {
		t.Fatalf("goal show: %s", out)
	}
	var w whereView
	ta.json("where", &w)
	if len(w.Goals) != 1 || w.Goals[0].Count != 1 || w.Goals[0].State != "ok" {
		t.Fatalf("where --json: %+v", w.Goals)
	}
	// The route alone changes without the text.
	ta.ok("goal set friend-a --to file:" + filepath.Join(dir, "elsewhere.txt"))
	if out := ta.ok("goal show friend-a"); !strings.Contains(out, "elsewhere.txt") || !strings.Contains(out, "keep going") {
		t.Fatalf("route change: %s", out)
	}
	if out := ta.ok("goal drop friend-a"); !strings.Contains(out, "GOAL-DROP OK name=friend-a dropped") {
		t.Fatalf("drop: %s", out)
	}
	if out := ta.ok("goal drop friend-a"); !strings.Contains(out, "unchanged") {
		t.Fatalf("drop again: %s", out)
	}
	if out := ta.ok("goal show"); !strings.Contains(out, "GOAL none") {
		t.Fatalf("show none: %s", out)
	}
}

func TestGoalSetRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	ok := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 8*1024+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ok, []byte("goal"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		if code, _, errs := ta.do(line); code == 0 || errs == "" {
			t.Errorf("%s: exit %d, %q", line, code, errs)
		}
	}
	if out := ta.ok("goal show"); !strings.Contains(out, "GOAL none") {
		t.Fatalf("a refused set stored someone: %s", out)
	}
}

func TestGoalSetDefaultRouteIsPrintedAndHelpNamesGoal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	ta.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_REMINDER_DIR" {
			return dir
		}
		return map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_PREFIX": "t-", "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	}
	ok := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(ok, []byte("goal"), 0o644); err != nil {
		t.Fatal(err)
	}
	ta.ok("init --readers reader-a,reader-b --members m1")
	if out := ta.ok("goal set friend-a --file " + ok); !strings.Contains(out, "route=file:"+filepath.Join(dir, "t-friend-a.txt")) {
		t.Fatalf("default route: %s", out)
	}
	if out := ta.ok("help goal"); !strings.Contains(out, "goal set") || !strings.Contains(out, "every 5") && !strings.Contains(out, "5\nminutes") {
		t.Fatalf("help goal: %s", out)
	}
	if out := ta.ok("help"); !strings.Contains(out, "nova-sprint goal show [<name>]") || !strings.Contains(out, "REMINDER <n> to <name>") {
		t.Fatalf("help: %s", out)
	}
}

// With no prefix the reminder header names the sprint by its view, sprint.
func TestReminderHeaderWithNoPrefix(t *testing.T) {
	t.Parallel()
	ta := newTestAppPrefix(t, "")
	dir := t.TempDir()
	text := filepath.Join(dir, "goal.txt")
	if err := os.WriteFile(text, []byte("keep going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("goal set friend-a --file " + text + " --to file:" + filepath.Join(dir, "r.txt"))
	ta.ok("start")
	ta.ok("tick")
	b, _ := os.ReadFile(filepath.Join(dir, "r.txt"))
	if !strings.HasPrefix(string(b), "REMINDER 1 to friend-a at "+t0.Format(time.RFC3339)+", sprint sprint, epoch 0\nkeep going\n") {
		t.Fatalf("file: %s", b)
	}
}

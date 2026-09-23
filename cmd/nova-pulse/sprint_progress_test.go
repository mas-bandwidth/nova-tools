package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestTaskStateChangeUpdatesSprintHashAndDoesNotLeaveItStale is #2684: the
// x/y/eta line is a read of sprint:<name> {done, units, percent, eta_minutes}.
// done, units and percent are nova-work set check's SET OK / SET DONE, not the
// closed-task count: one task can cover many units, and a closed task can
// leave its units unsatisfied. percent is the tool's (61, not Percent's 62).
// A later close moves eta and must not put a task ratio back, and must not
// leave the previous eta in the hash.
func TestTaskStateChangeUpdatesSprintHashAndDoesNotLeaveItStale(t *testing.T) {
	mr := miniredis.RunT(t)
	deps := sprintDeps{
		open: func(addr, user, password string) (sprint.Store, error) {
			return sprint.Dial(addr, user, password)
		},
	}
	addr := []string{"--store", mr.Addr()}
	now := time.Date(2026, 9, 22, 17, 0, 0, 0, time.UTC)
	run := func(args ...string) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := runSprint(args, &out, &errOut, now, deps); code != 0 {
			t.Fatalf("%v exited %d\nstdout: %s\nstderr: %s", args, code, out.String(), errOut.String())
		}
	}

	run(append([]string{"open", "--name", "fixes", "--goal", "the fixes day"}, addr...)...)
	run(append([]string{"add", "--name", "fixes", "--id", "a", "--ref", "o/n#1", "--kind", "fix",
		"--owner", "johnny", "--est", "60", "--leased-at", "2026-09-22T16:00:00Z"}, addr...)...)
	run(append([]string{"add", "--name", "fixes", "--id", "b", "--ref", "o/n#2", "--kind", "fix",
		"--owner", "johnny", "--est", "60"}, addr...)...)
	// Two open fixes on one lane: the wall is the sum. No evaluation yet, so
	// the task count is not the unit count.
	assertSprintProgress(t, mr, "fixes", "0", "0", "0", "120")

	st, err := sprint.Dial(mr.Addr(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const live = "SET OK units=42 ready=7 blocked=9 owned=0\nSET DONE done=26 percent=61\n"
	if err := sprint.RecordAcceptance(context.Background(), st, "fixes", live); err != nil {
		t.Fatal(err)
	}
	// 26/42 is the evaluated set. Two tasks would be 0/2, and Percent(26, 42)
	// is 62; 61 is SET DONE's percent, kept as printed.
	assertSprintProgress(t, mr, "fixes", "26", "42", "61", "120")

	run(append([]string{"close", "--name", "fixes", "--task", "a", "--evidence", "merged abc",
		"--at", "2026-09-22T16:30:00Z"}, addr...)...)
	// a took 30 minutes, not its estimate of 60. b is the same kind, so the
	// remaining wall is that actual (30), not b's raw estimate (60) and not the
	// previous hash (120). Closing a does not make done 1 or units 2.
	assertSprintProgress(t, mr, "fixes", "26", "42", "61", "30")

	run(append([]string{"close", "--name", "fixes", "--task", "b", "--evidence", "merged def",
		"--at", "2026-09-22T17:00:00Z"}, addr...)...)
	// Both tasks are closed. A task-derived hash would now read 2/2/100 and
	// would have left eta at 30 if the close did not rewrite it. The acceptance
	// set has not moved, and the wall has.
	assertSprintProgress(t, mr, "fixes", "26", "42", "61", "0")

	const later = "SET OK units=42 ready=6 blocked=9 owned=0\nSET DONE done=27 percent=99\n"
	if err := sprint.RecordAcceptance(context.Background(), st, "fixes", later); err != nil {
		t.Fatal(err)
	}
	// 99 is not Percent(27, 42). The next evaluation replaces the counts; the
	// closed tasks still do not.
	assertSprintProgress(t, mr, "fixes", "27", "42", "99", "0")
}

func assertSprintProgress(t *testing.T, mr *miniredis.Miniredis, name, done, units, percent, eta string) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	m, err := rdb.HGetAll(context.Background(), "sprint:"+name).Result()
	if err != nil {
		t.Fatalf("HGETALL sprint:%s: %v", name, err)
	}
	if m["goal"] == "" {
		t.Fatalf("sprint:%s lost its goal when the progress fields were written: %#v", name, m)
	}
	got := [4]string{m["done"], m["units"], m["percent"], m["eta_minutes"]}
	want := [4]string{done, units, percent, eta}
	if got != want {
		t.Fatalf("sprint:%s = done %q units %q percent %q eta_minutes %q, want %q %q %q %q\nhash: %#v",
			name, got[0], got[1], got[2], got[3], want[0], want[1], want[2], want[3], m)
	}
}

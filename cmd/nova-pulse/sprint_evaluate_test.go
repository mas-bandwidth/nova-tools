package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestSprintEvaluateIsTheProductionCallerOfRecordAcceptance is the hold on #2700 at
// 939a4155: RecordAcceptance had no caller, so outside a test done/units/percent stayed
// 0/0/0. `sprint evaluate` runs nova-work set check --evaluate and records it; --every is
// the reconciler that keeps doing so; a refused check (exit 2) leaves the last good
// evaluation on the hash rather than zeroing it.
func TestSprintEvaluateIsTheProductionCallerOfRecordAcceptance(t *testing.T) {
	mr := miniredis.RunT(t)
	outputs := []struct {
		out  string
		code int
	}{
		{"SET OK units=42 ready=7 blocked=9 owned=0\nSET DONE done=26 percent=61\n", 0},
		{"SET EVAL u1 holds=yes why=merged\nSET DUPLICATE u9\nSET OK units=42 ready=6 blocked=9 owned=0\nSET DONE done=27 percent=64\n", 1},
		{"", 2},
	}
	var calls [][]string
	var slept []time.Duration
	deps := sprintDeps{
		open: func(addr, user, password string) (sprint.Store, error) {
			return sprint.Dial(addr, user, password)
		},
		setCheck: func(ctx context.Context, bin string, args []string) (string, int, error) {
			calls = append(calls, append([]string{bin}, args...))
			o := outputs[0]
			outputs = outputs[1:]
			return o.out, o.code, nil
		},
		sleep: func(d time.Duration) { slept = append(slept, d) },
	}
	addr := []string{"--store", mr.Addr()}
	now := time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC)
	run := func(want int, args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := runSprint(append(args, addr...), &out, &errOut, now, deps); code != want {
			t.Fatalf("%v exited %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out.String(), errOut.String())
		}
		return out.String()
	}

	run(0, "open", "--name", "fixes", "--goal", "the fixes day")
	run(0, "add", "--name", "fixes", "--id", "a", "--ref", "o/n#1", "--kind", "fix", "--owner", "johnny", "--est", "60")
	assertSprintProgress(t, mr, "fixes", "0", "0", "0", "60")

	out := run(0, "evaluate", "--name", "fixes", "--work-set", "/w/set.lisp", "--nova-work", "/bin/nova-work", "--base", "dev")
	if !strings.Contains(out, "SPRINT EVALUATED fixes 26/42 61%") {
		t.Fatalf("stdout %q", out)
	}
	want := "/bin/nova-work set check --file /w/set.lisp --evaluate --base dev"
	if got := strings.Join(calls[0], " "); got != want {
		t.Fatalf("ran %q, want %q", got, want)
	}
	assertSprintProgress(t, mr, "fixes", "26", "42", "61", "60")

	// The reconciler: two rounds, one sleep between. Round one is findings (exit 1), which
	// still count; round two is a refusal, which is reported and leaves 27/42 64% standing.
	run(1, "evaluate", "--name", "fixes", "--work-set", "/w/set.lisp", "--every", "1m", "--rounds", "2")
	if len(slept) != 1 || slept[0] != time.Minute {
		t.Fatalf("slept %v, want one minute between two rounds", slept)
	}
	if len(calls) != 3 {
		t.Fatalf("set check ran %d times, want 3", len(calls))
	}
	assertSprintProgress(t, mr, "fixes", "27", "42", "64", "60")

	// Closing the task moves eta only; the evaluated counts stay.
	run(0, "close", "--name", "fixes", "--task", "a", "--evidence", "merged abc", "--at", "2026-09-23T16:30:00Z")
	assertSprintProgress(t, mr, "fixes", "27", "42", "64", "0")
}

func TestSprintEvaluateRefusesWithoutAWorkSet(t *testing.T) {
	mr := miniredis.RunT(t)
	deps := sprintDeps{open: func(addr, user, password string) (sprint.Store, error) { return sprint.Dial(addr, user, password) }}
	var out, errOut bytes.Buffer
	if code := runSprint([]string{"evaluate", "--store", mr.Addr()}, &out, &errOut, time.Time{}, deps); code != 2 {
		t.Fatalf("exit %d, want 2; stderr %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "work-set") {
		t.Fatalf("stderr %q does not name --work-set", errOut.String())
	}
}

// TestSprintEvaluateRecordsTheRealNovaWorkSetCheck is stella's hold at 91658c35: the fake
// above accepts any argv and hands back a synthetic SET DONE, so it could not catch the
// caller passing a flag the real `nova-work set check` refuses, or the real command not
// printing SET DONE. Here the caller runs the nova-work built from this tree through the
// production runner (runSetCheck), with the exact argv sprint evaluate builds, over a work
// set with no gh-evaluable criterion so no network is touched: two of four units closed
// must land on sprint:<name> as 2/4 50%.
func TestSprintEvaluateRecordsTheRealNovaWorkSetCheck(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-work")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-work")
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nova-work: %v\n%s", err, out)
	}
	set := filepath.Join(dir, "set.lisp")
	if err := os.WriteFile(set, []byte(`(work-set "real" :repo "mas-bandwidth/nova-tools" :units (
  (unit "a" :status "closed" :title "landed")
  (unit "b" :status "closed" :title "landed too")
  (unit "c" :needs ("a" "b") :owner "stella" :lane "work" :title "ready")
  (unit "d" :needs ("c") :title "blocked")))
`), 0o644); err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	deps := sprintDeps{
		open:     func(addr, user, password string) (sprint.Store, error) { return sprint.Dial(addr, user, password) },
		setCheck: runSetCheck,
		sleep:    func(time.Duration) {},
	}
	now := time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC)
	run := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := runSprint(append(args, "--store", mr.Addr()), &out, &errOut, now, deps); code != 0 {
			t.Fatalf("%v exited %d\nstdout: %s\nstderr: %s", args, code, out.String(), errOut.String())
		}
		return out.String()
	}
	run("open", "--name", "real", "--goal", "the real producer")
	run("add", "--name", "real", "--id", "a", "--ref", "o/n#1", "--kind", "fix", "--owner", "stella", "--est", "60")
	assertSprintProgress(t, mr, "real", "0", "0", "0", "60")

	out := run("evaluate", "--name", "real", "--work-set", set, "--nova-work", bin, "--base", "dev", "--cache", filepath.Join(dir, "cache"))
	if !strings.Contains(out, "SPRINT EVALUATED real 2/4 50%") {
		t.Fatalf("stdout %q, want the real set check's 2/4 50%% recorded", out)
	}
	assertSprintProgress(t, mr, "real", "2", "4", "50", "60")
}

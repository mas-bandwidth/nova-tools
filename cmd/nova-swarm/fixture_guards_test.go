package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE FIXTURE'S CAP IS ENFORCED, NOT REMEMBERED (#132, a LOW of the #126 delta read).
//
// `FAKE-AWAIT-NOTE n` holds the fake worker until a note is delivered to its job directory or
// until n seconds are up, and the worker's whole hold must be UNDER the deadline the job runs
// under. A hold equal to the deadline is no bound at all: the wait and the reaper come due
// together, so the worker is killed instead of publishing, and the run reads as rule 7's
// `end=killed` with a re-queue and a second reap -- while the one fact that explains it, that
// this worker was holding for a note nobody sent, is nowhere. That was the silent kill #126
// closed, and until now the only thing that kept it closed was a number (`FAKE-AWAIT-NOTE 20`
// under a 30s deadline) that a future fixture had to REMEMBER. It is refused instead, before
// the job is queued and again before the run, and the refusal names the numbers.
//
// This is fixture safety, not a rule: SPEC-SWARM says nothing about the fake's directives, and
// grep finds no `FAKE-` in it.

// awaitNoteCap is the refusal, or nil. `deadline` is the deadline the job will actually run
// under, which benchJobDeadline answers.
//
// THE CAP IS OF THE WORKER'S HOLD, NOT OF ONE DIRECTIVE (the #140 read, finding 1). The fake
// sleeps BEFORE it waits -- `FAKE-SLEEP` at testdata/fakeharness/main.go's
// `time.Sleep(time.Duration(n) * time.Second)`, then `awaitNote(job, ...)` -- so
// `FAKE-SLEEP 15` beside `FAKE-AWAIT-NOTE 20` is a 35s hold, which a cap that measured the
// wait alone let through under a 30s deadline: red end to end at 78.79s, `end=killed`,
// `requeued=true`, `reaped=2`, and no "the wait gave up" in the harness log. The two
// directives are summed. Nothing else in the fake holds a clock ahead of the wait: the
// foreground child returns before the sleep (`FAKE_FOREGROUND_CHILD=1` returns at once), and
// every other directive is a write or an exit.
func awaitNoteCap(task string, deadline time.Duration) error {
	n, ok := fixtureNumber(task, "FAKE-AWAIT-NOTE")
	if !ok {
		return nil
	}
	sleep, _ := fixtureNumber(task, "FAKE-SLEEP")
	hold := time.Duration(sleep+n) * time.Second
	if deadline <= 0 || hold < deadline {
		return nil
	}
	before := ""
	if sleep > 0 {
		before = fmt.Sprintf(" after FAKE-SLEEP %d", sleep)
	}
	return fmt.Errorf("FAKE-AWAIT-NOTE %d%s: a %s hold is not under this job's %s deadline, "+
		"so the reaper comes due with the wait and the worker is killed instead of publishing "+
		"(the silent end=killed of #126); cap the hold under %ds",
		n, before, hold, deadline, int(deadline.Seconds()))
}

// benchJobDeadline is the deadline the job this `add` queues will run under: the `--deadline`
// of this add if it carries one, and otherwise the worker description's own default. The
// description is READ rather than remembered, so a bench that rewrote its deadline is capped
// against the deadline it now has.
func benchJobDeadline(workerFile string, extra []string) (time.Duration, error) {
	if d, ok := flagAfter(extra, "--deadline"); ok {
		return time.ParseDuration(d)
	}
	return workerDefaultDeadline(workerFile)
}

// workerDefaultDeadline is the description's own default, the second half of what
// `taskDeadline` (internal/swarm/supervise.go) resolves.
func workerDefaultDeadline(workerFile string) (time.Duration, error) {
	raw, err := os.ReadFile(workerFile)
	if err != nil {
		return 0, err
	}
	var desc struct {
		Deadline string `json:"deadline"`
	}
	if err := json.Unmarshal(raw, &desc); err != nil {
		return 0, err
	}
	if desc.Deadline == "" {
		return 0, nil
	}
	return time.ParseDuration(desc.Deadline)
}

// flagAfter is `--name value` or `--name=value`, LAST one winning, which is what the flag
// package does with a repeated flag and therefore what `b.run("--worker", other)` means.
func flagAfter(args []string, name string) (string, bool) {
	value, found := "", false
	for i, arg := range args {
		switch {
		case arg == name && i+1 < len(args):
			value, found = args[i+1], true
		case strings.HasPrefix(arg, name+"="):
			value, found = strings.TrimPrefix(arg, name+"="), true
		}
	}
	return value, found
}

// capPendingTasks is the SAME cap at the point the job's deadline is actually resolved: every
// task still pending in the pool, measured against `taskDeadline`'s own rule -- the task's own
// deadline where it names one, otherwise the description THIS run hands the dispatcher.
//
// `bench.add` catches a bad fixture earlier and names it at the line that wrote it, but the
// deadline it reads is the one standing AT ADD TIME. This one closes the rest of what the #140
// read named (finding 2): a `rewriteWorker` of `deadline` after the add, a `b.run("--worker",
// other)` whose description has a different one, and the jobs that never went through `add` at
// all -- `batch`, and `requeue --task-file`.
//
// WHAT STAYS UNCOVERED, plainly: a `run` invoked as `b.swarm("run", ...)` rather than `b.run`
// (sandbox_seam_test.go has one) is not scanned, and neither is a description rewritten
// between this scan and the dispatcher's own read of it. Both are a line of test code away
// from the fixture that would have to do it deliberately; the silent kill this guards against
// came from a number nobody re-derived, which is now derived twice.
func capPendingTasks(pool, workerFile string) error {
	tasks, err := filepath.Glob(filepath.Join(pool, swarm.Pending, "*.task"))
	if err != nil {
		return err
	}
	fallback, err := workerDefaultDeadline(workerFile)
	if err != nil {
		return err
	}
	for _, file := range tasks {
		text, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		deadline := fallback
		if own, err := workerDefaultDeadline(strings.TrimSuffix(file, ".task") + ".json"); err == nil && own > 0 {
			deadline = own
		}
		if err := awaitNoteCap(string(text), deadline); err != nil {
			return fmt.Errorf("%s: %w", strings.TrimSuffix(filepath.Base(file), ".task"), err)
		}
	}
	return nil
}

// fixtureNumber reads `NAME n` out of a task the way the fake harness reads it, so the cap
// and the fake agree on what the directive says.
func fixtureNumber(task, name string) (int, bool) {
	for _, line := range strings.Split(task, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, name) {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, name)) + " 0")
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func TestTheBenchRefusesAWaitThatReachesTheJobsOwnDeadline(t *testing.T) {
	for _, c := range []struct {
		name     string
		task     string
		deadline time.Duration
		refused  bool
	}{
		// THE CASE THE #126 READ NAMED: `FAKE-AWAIT-NOTE 30` under the bench's own 30s.
		{"a wait equal to the deadline", "a task\nFAKE-AWAIT-NOTE 30\n", 30 * time.Second, true},
		{"a wait past the deadline", "a task\nFAKE-AWAIT-NOTE 45\n", 30 * time.Second, true},
		{"a wait equal to a per-job deadline", "a task\nFAKE-AWAIT-NOTE 3\n", 3 * time.Second, true},
		// THE CASE THE #140 READ PROVED RED END TO END: the sleep comes first, so the hold is
		// 35s under a 30s deadline though the wait alone is 20.
		{"a sleep and a wait that reach the deadline together", "a task\nFAKE-SLEEP 15\nFAKE-AWAIT-NOTE 20\n", 30 * time.Second, true},
		{"a sleep and a wait exactly at the deadline", "a task\nFAKE-SLEEP 10\nFAKE-AWAIT-NOTE 20\n", 30 * time.Second, true},
		{"a sleep and a wait that stay under it", "a task\nFAKE-SLEEP 5\nFAKE-AWAIT-NOTE 20\n", 30 * time.Second, false},
		{"the fixture as it stands", "a task\nFAKE-AWAIT-NOTE 20\nFAKE-FINDINGS 1\n", 30 * time.Second, false},
		{"a one-second wait", "a task\nFAKE-AWAIT-NOTE 1\n", 30 * time.Second, false},
		{"no directive at all", "a task\nFAKE-FINDINGS 1\n", 30 * time.Second, false},
		// A sleep with no wait is a DELIBERATE fixture -- the reaped-worker tests sleep 30
		// under a 3s deadline on purpose -- and is no business of this cap.
		{"a sleep past the deadline with no wait", "a task\nFAKE-SLEEP 30\n", 3 * time.Second, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := awaitNoteCap(c.task, c.deadline)
			if c.refused != (err != nil) {
				t.Fatalf("refused=%t, wanted %t, for this task under a %s deadline: %v", err != nil, c.refused, c.deadline, err)
			}
			if err == nil {
				return
			}
			// THE REFUSAL NAMES THE NUMBERS, because a reader of the failure has to know
			// which of them to move: the wait, the sleep that precedes it, the hold they
			// come to, and the deadline it has to fit under.
			n, _ := fixtureNumber(c.task, "FAKE-AWAIT-NOTE")
			sleep, _ := fixtureNumber(c.task, "FAKE-SLEEP")
			want := []string{strconv.Itoa(n), strconv.Itoa(n + sleep), strconv.Itoa(int(c.deadline.Seconds()))}
			if sleep > 0 {
				want = append(want, "FAKE-SLEEP "+strconv.Itoa(sleep))
			}
			for _, w := range want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal does not name %q, so a reader cannot tell which number to move:\n%s", w, err)
				}
			}
		})
	}
}

// The other half: the deadline the cap is measured against is the one the job RUNS under, so
// a per-job `--deadline` is what a task queued with one is checked against -- otherwise the
// three-second jobs in this package would be capped against a thirty-second bench.
func TestTheDeadlineACappedJobIsMeasuredAgainstIsTheOneItRunsUnder(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker.json")
	if err := os.WriteFile(worker, []byte(`{"name":"fake-1","deadline":"30s"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name  string
		extra []string
		want  time.Duration
	}{
		{"the description's own default", nil, 30 * time.Second},
		{"a per-job --deadline", []string{"--deadline", "3s"}, 3 * time.Second},
		{"a per-job --deadline=", []string{"--deadline=5s"}, 5 * time.Second},
		{"the last --deadline wins", []string{"--deadline", "9s", "--deadline", "4s"}, 4 * time.Second},
		{"another flag entirely", []string{"--label", "x"}, 30 * time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := benchJobDeadline(worker, c.extra)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("the job runs under %s, and the cap would be measured against %s", c.want, got)
			}
		})
	}

	// And the two together, on the shape the package actually queues: a three-second job
	// whose wait is the bench's twenty is refused, though twenty is fine under thirty.
	task := "a worker that reads its notes\nFAKE-AWAIT-NOTE 20\nFAKE-FINDINGS 1\n"
	deadline, err := benchJobDeadline(worker, []string{"--deadline", "3s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := awaitNoteCap(task, deadline); err == nil {
		t.Error("a twenty-second wait inside a three-second job was allowed through")
	}
	if deadline, err = benchJobDeadline(worker, nil); err != nil {
		t.Fatal(err)
	}
	if err := awaitNoteCap(task, deadline); err != nil {
		t.Errorf("the fixture as it stands was refused: %v", err)
	}
}

// THE SECOND POINT THE CAP IS APPLIED (the #140 read, finding 2): the pending pool, measured
// against the description THIS run hands the dispatcher and the task's OWN deadline where it
// has one -- `taskDeadline`'s rule, at the point that rule is resolved. This is what catches
// the add-then-rewrite order, another `--worker`, and the jobs `batch` and `requeue` queue
// without going through `add` at all.
func TestThePendingPoolIsCappedAgainstTheWorkerTheRunWillUse(t *testing.T) {
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool", swarm.Pending)
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}
	queue := func(id, text, deadline string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(pool, id+".task"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		// The sidecar as `add` writes it: a task with no deadline of its own carries "".
		if err := os.WriteFile(filepath.Join(pool, id+".json"),
			[]byte(`{"id":"`+id+`","deadline":"`+deadline+`"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	worker := func(name, deadline string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(`{"name":"fake-1","deadline":"`+deadline+`"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	long, short := worker("worker.json", "30s"), worker("short.json", "20s")
	// A job queued by `batch`, which never passes through `add`: fine under thirty seconds.
	queue("20260912T000000Z-batched", "a batched task\nFAKE-AWAIT-NOTE 20\n", "")
	if err := capPendingTasks(filepath.Join(dir, "pool"), long); err != nil {
		t.Fatalf("a twenty-second wait under a thirty-second description was refused: %v", err)
	}
	// THE SAME POOL, run with another description: the wait now reaches the deadline.
	err := capPendingTasks(filepath.Join(dir, "pool"), short)
	if err == nil {
		t.Fatal("a twenty-second wait under a twenty-second description was allowed through")
	}
	for _, want := range []string{"20260912T000000Z-batched", "20", "FAKE-AWAIT-NOTE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, err)
		}
	}
	// A task with a deadline of ITS OWN is measured against that one, description or not.
	queue("20260912T000001Z-own", "a task with its own deadline\nFAKE-SLEEP 5\nFAKE-AWAIT-NOTE 20\n", "5s")
	err = capPendingTasks(filepath.Join(dir, "pool"), long)
	if err == nil || !strings.Contains(err.Error(), "20260912T000001Z-own") {
		t.Fatalf("the task's own five-second deadline was not what its twenty-five-second hold was measured against: %v", err)
	}
}

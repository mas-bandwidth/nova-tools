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
)

// THE FIXTURE'S CAP IS ENFORCED, NOT REMEMBERED (#132, a LOW of the #126 delta read).
//
// `FAKE-AWAIT-NOTE n` holds the fake worker until a note is delivered to its job directory or
// until n seconds are up, and n must be UNDER the deadline the job runs under. A bound equal
// to the deadline is no bound at all: the wait and the reaper come due together, so the
// worker is killed instead of publishing, and the run reads as rule 7's `end=killed` with a
// re-queue and a second reap -- while the one fact that explains it, that this worker was
// holding for a note nobody sent, is nowhere. That was the silent kill #126 closed, and until
// now the only thing that kept it closed was a number (`FAKE-AWAIT-NOTE 20` under a 30s
// deadline) that a future fixture had to REMEMBER. `bench.add` refuses it instead, before the
// job is queued, and the refusal names BOTH numbers so a reader knows which one to move.
//
// This is fixture safety, not a rule: SPEC-SWARM says nothing about the fake's directives.

// awaitNoteCap is the refusal, or nil. `deadline` is the deadline the job will actually run
// under, which benchJobDeadline answers.
func awaitNoteCap(task string, deadline time.Duration) error {
	n, ok := fixtureNumber(task, "FAKE-AWAIT-NOTE")
	if !ok {
		return nil
	}
	wait := time.Duration(n) * time.Second
	if deadline <= 0 || wait < deadline {
		return nil
	}
	return fmt.Errorf("FAKE-AWAIT-NOTE %d: a %s wait is not under this job's %s deadline, "+
		"so the reaper comes due with the wait and the worker is killed instead of publishing "+
		"(the silent end=killed of #126); cap the directive under %ds",
		n, wait, deadline, int(deadline.Seconds()))
}

// benchJobDeadline is the deadline the job this `add` queues will run under: the `--deadline`
// of this add if it carries one, and otherwise the worker description's own default. The
// description is READ rather than remembered, so a bench that rewrote its deadline is capped
// against the deadline it now has.
func benchJobDeadline(workerFile string, extra []string) (time.Duration, error) {
	for i, arg := range extra {
		switch {
		case arg == "--deadline" && i+1 < len(extra):
			return time.ParseDuration(extra[i+1])
		case strings.HasPrefix(arg, "--deadline="):
			return time.ParseDuration(strings.TrimPrefix(arg, "--deadline="))
		}
	}
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
		{"the fixture as it stands", "a task\nFAKE-AWAIT-NOTE 20\nFAKE-FINDINGS 1\n", 30 * time.Second, false},
		{"a one-second wait", "a task\nFAKE-AWAIT-NOTE 1\n", 30 * time.Second, false},
		{"no directive at all", "a task\nFAKE-FINDINGS 1\n", 30 * time.Second, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := awaitNoteCap(c.task, c.deadline)
			if c.refused != (err != nil) {
				t.Fatalf("refused=%t, wanted %t, for this task under a %s deadline: %v", err != nil, c.refused, c.deadline, err)
			}
			if err == nil {
				return
			}
			// THE REFUSAL NAMES BOTH NUMBERS, because a reader of the failure has to know
			// which of the two to move.
			n, _ := fixtureNumber(c.task, "FAKE-AWAIT-NOTE")
			for _, want := range []string{strconv.Itoa(n), strconv.Itoa(int(c.deadline.Seconds()))} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q, so a reader cannot tell which number to move:\n%s", want, err)
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

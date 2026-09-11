package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EVERY REFUSAL SAYS WHAT THE INPUT WANTS, AND ONE RUN NAMES EVERY INDEPENDENT PROBLEM
// (ONBOARDING point 2). A refusal naming only the fault has moved the guessing onto the
// reader, and sending a first run back three times for three independent flags is three
// refusals the first one already knew about.

func TestAddWithoutItsBudgetsIsRefusedAndSaysWhatTheyWant(t *testing.T) {
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool")
	runSwarm(t, "quickstart", "--pool", pool)
	task := filepath.Join(dir, "task.md")
	write(t, task, "read this pull request\n")

	// Rule 4 and rule 13: neither budget has a default, and a run missing both says so
	// about both, in one go.
	exit, _, stderr := runSwarm(t, "add", "--pool", pool, "--task", task)
	if exit != 2 {
		t.Errorf("an add with no --files and no --tokens exits %d, want 2", exit)
	}
	for _, want := range []string{"--files is required", "--tokens is required", "refusing to guess"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not contain %q:\n%s", want, stderr)
		}
	}
	if lines := strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") + 1; lines < 2 {
		t.Errorf("two independent problems want two lines in one run, got %d:\n%s", lines, stderr)
	}

	// Zero is refused for both: a worker that may open no file is a worker asked for a plan.
	if exit, _, stderr = runSwarm(t, "add", "--pool", pool, "--task", task, "--files", "0", "--tokens", "10"); exit != 2 {
		t.Errorf("--files 0 exits %d, want 2; stderr: %s", exit, stderr)
	}
	if exit, _, stderr = runSwarm(t, "add", "--pool", pool, "--task", task, "--files", "5", "--tokens", "0"); exit != 2 {
		t.Errorf("--tokens 0 exits %d, want 2; stderr: %s", exit, stderr)
	}
	// And `unmetered` is the caller's statement that there is no accounting at all.
	if exit, stdout, stderr := runSwarm(t, "add", "--pool", pool, "--task", task, "--files", "5", "--tokens", "unmetered"); exit != 0 {
		t.Errorf("--tokens unmetered exits %d, want 0; stderr: %s%s", exit, stdout, stderr)
	} else if !strings.Contains(stdout, "tokens=unmetered") {
		t.Errorf("an unmetered task says so on its ADD line:\n%s", stdout)
	}
}

// --workers above the cap is a REFUSAL, not a silent clamp: a caller who asked for 200
// workers has a belief about throughput that a note at the top of a log does not correct.
func TestWorkersAboveTheCapIsRefused(t *testing.T) {
	pool := filepath.Join(t.TempDir(), "pool")
	runSwarm(t, "quickstart", "--pool", pool)
	exit, _, stderr := runSwarm(t, "run", "--pool", pool, "--workers", "65", "--hours", "1", "--worker", "nowhere.json")
	if exit != 2 {
		t.Errorf("--workers 65 exits %d, want 2", exit)
	}
	if !strings.Contains(stderr, "capped at 64") {
		t.Errorf("the refusal does not name the cap:\n%s", stderr)
	}
}

// `supervise` is run's child and nobody's verb.
func TestSuperviseTypedByHandIsRefused(t *testing.T) {
	pool := filepath.Join(t.TempDir(), "pool")
	runSwarm(t, "quickstart", "--pool", pool)
	exit, _, stderr := runSwarm(t, "supervise", "--pool", pool, "--task", "whatever", "--slot", "1", "--nonce", "abc123abc123", "--worker", "w.json")
	if exit != 2 {
		t.Errorf("a hand-typed supervise exits %d, want 2", exit)
	}
	if !strings.Contains(stderr, "run: nova-swarm help") {
		t.Errorf("the refusal names no door:\n%s", stderr)
	}
}

// An unknown verb and a flag typo cost ONE line each, never the banner.
func TestAnUnusableInvocationCostsOneLine(t *testing.T) {
	for _, args := range [][]string{{"tirage"}, {"status", "--pooll", "x"}} {
		exit, stdout, stderr := runSwarm(t, args...)
		if exit != 2 {
			t.Errorf("`%s` exits %d, want 2", strings.Join(args, " "), exit)
		}
		if stdout != "" {
			t.Errorf("`%s` wrote to stdout: %q", strings.Join(args, " "), stdout)
		}
		if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 {
			t.Errorf("`%s` printed %d lines, want 1:\n%s", strings.Join(args, " "), len(lines), stderr)
		}
	}
}

// A missing or empty key file is exit 2 WITH THE COMMAND THAT CREATES IT, and the refusal
// never prints the path's contents.
func TestAnEmptyKeyFileIsRefusedWithTheCommandThatWritesIt(t *testing.T) {
	b := newBench(t)
	write(t, b.keyFile, "\n")
	exit, _, stderr := b.run()
	if exit != 2 {
		t.Errorf("an empty key file exits %d, want 2", exit)
	}
	for _, want := range []string{"is empty", "chmod 600"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not contain %q:\n%s", want, stderr)
		}
	}
}

// Every listing is a cap and a count: at most --max item lines, in the order the verb
// produced them -- a cap is a PREFIX, never a sample -- then one MORE line naming the
// remedy, and a count line that is the truth about the pool.
func TestACappedListingIsAPrefixWithAMoreLine(t *testing.T) {
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool")
	runSwarm(t, "quickstart", "--pool", pool)
	task := filepath.Join(dir, "task.md")
	write(t, task, "a task\n")
	for i := 0; i < 5; i++ {
		if exit, _, stderr := runSwarm(t, "add", "--pool", pool, "--task", task, "--files", "3", "--tokens", "100"); exit != 0 {
			t.Fatalf("add exited %d: %s", exit, stderr)
		}
	}
	exit, stdout, _ := runSwarm(t, "status", "--pool", pool, "--max", "2")
	if exit != 0 {
		t.Fatalf("status exited %d", exit)
	}
	if n := strings.Count(stdout, "STATUS TASK"); n != 2 {
		t.Errorf("--max 2 printed %d item lines, want 2:\n%s", n, stdout)
	}
	if !strings.Contains(stdout, "STATUS MORE kind=task shown=2 total=5") {
		t.Errorf("a capped listing wants one MORE line naming the remedy:\n%s", stdout)
	}
	if !strings.Contains(stdout, "pending=5") {
		t.Errorf("the count is the truth about the POOL, never about the output:\n%s", stdout)
	}
	// 0 means all: a ceiling a caller cannot lift is a tool deciding what its user may see.
	_, stdout, _ = runSwarm(t, "status", "--pool", pool, "--max", "0")
	if n := strings.Count(stdout, "STATUS TASK"); n != 5 {
		t.Errorf("--max 0 printed %d item lines, want 5", n)
	}
	if strings.Contains(stdout, "STATUS MORE") {
		t.Error("--max 0 elides nothing, so there is no MORE line")
	}
}

// RUN NOTE is EXACTLY ONE remedy line.
func TestRunNoteIsExactlyOneLine(t *testing.T) {
	b := newBench(t)
	b.add("a task that finishes\nFAKE-FINDINGS 1\n")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	notes := 0
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "RUN NOTE ") {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("a pass printed %d RUN NOTE lines, want exactly 1:\n%s", notes, stdout)
	}
}

// `triage --batch` of an id no sidecar carries is a refusal at exit 1, and a task queued by
// `add` beside a batch is not in it.
func TestBatchMembershipIsTheSidecar(t *testing.T) {
	b := newBench(t)
	tasks := filepath.Join(b.dir, "tasks")
	write(b.t, filepath.Join(tasks, "one.md"), "first task\nFAKE-FINDINGS 1\n")
	write(b.t, filepath.Join(tasks, "two.md"), "second task\nFAKE-FINDINGS 1\n")
	exit, stdout, stderr := b.swarm("batch", "--pool", b.pool, "--tasks", tasks, "--files", "3", "--tokens", "1000")
	if exit != 0 {
		t.Fatalf("batch exited %d: %s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "BATCH OK") || !strings.Contains(stdout, "tasks=2") {
		t.Fatalf("batch wants one BATCH OK line naming its id and its count:\n%s", stdout)
	}
	batchID := field(t, stdout, "id=")
	b.add("a task queued by add, not in the batch\nFAKE-FINDINGS 1\n")
	if exit, stdout, stderr = b.run("--workers", "3"); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	exit, stdout, _ = b.swarm("triage", "--pool", b.pool, "--batch", batchID)
	if exit != 0 {
		t.Fatalf("triage --batch exited %d", exit)
	}
	if !strings.Contains(stdout, "TRIAGE BATCH batch="+batchID+" reports=2") {
		t.Errorf("triage --batch walks exactly the jobs whose sidecar carries it:\n%s", stdout)
	}
	exit, _, stderr = b.swarm("triage", "--pool", b.pool, "--batch", "20260101T000000Z-nothing-abcdef")
	if exit != 1 {
		t.Errorf("triage --batch of an unknown id exits %d, want 1", exit)
	}
	if !strings.Contains(stderr, "TRIAGE REFUSED") {
		t.Errorf("an unknown batch id is a TRIAGE REFUSED:\n%s", stderr)
	}
}

// An empty task directory is a BATCH REFUSED at exit 1, and one unreadable file queues
// nothing at all: a batch is all of its tasks or none.
func TestABatchIsAllOfItsTasksOrNone(t *testing.T) {
	b := newBench(t)
	empty := filepath.Join(b.dir, "empty")
	write(b.t, filepath.Join(empty, "keep"), "")
	if err := removeFile(filepath.Join(empty, "keep")); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := b.swarm("batch", "--pool", b.pool, "--tasks", empty, "--files", "3", "--tokens", "1000")
	if exit != 1 {
		t.Errorf("a batch over a directory with no task file exits %d, want 1; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "BATCH REFUSED") {
		t.Errorf("stderr wants a BATCH REFUSED line:\n%s", stderr)
	}
}

func removeFile(path string) error { return os.Remove(path) }

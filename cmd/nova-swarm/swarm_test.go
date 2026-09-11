package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// THE CONTRACT TESTS. Every one of them runs against the FAKE HARNESS binary on PATH,
// inside t.TempDir(), with no network and no provider -- so the dispatcher is tested end to
// end and nothing here costs a token or reaches a key that is worth anything.
//
// CONTRIBUTING.md: test code is code. No test here reaches outside t.TempDir(), none
// touches the network, and none matches a process by its command line.

// bench is one pool, one worker description, one key file and a fake harness on PATH.
type bench struct {
	t       *testing.T
	dir     string
	pool    string
	binary  string
	worker  string
	keyFile string
	path    string
}

const fakeKey = "sk-fake-0123456789-not-a-key"

func newBench(t *testing.T) *bench {
	t.Helper()
	dir := t.TempDir()
	b := &bench{t: t, dir: dir, pool: filepath.Join(dir, "pool")}
	if err := os.MkdirAll(b.pool, 0o755); err != nil {
		t.Fatal(err)
	}
	b.binary = build(t, dir, "nova-swarm", "./cmd/nova-swarm")
	harnessDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	build(t, harnessDir, "fake-harness", "./cmd/nova-swarm/testdata/fakeharness")
	b.path = harnessDir + string(os.PathListSeparator) + os.Getenv("PATH")

	home := filepath.Join(dir, "worker-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, "AGENTS.md"), "the worker's own self, copied one way into every slot\n")

	b.keyFile = filepath.Join(dir, "key")
	write(t, b.keyFile, "FAKE_KEY="+fakeKey+"\na second line nothing may read\n")
	if err := os.Chmod(b.keyFile, 0o600); err != nil {
		t.Fatal(err)
	}

	b.worker = filepath.Join(dir, "worker.json")
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": "fake-model",
		"env_var": "FAKE_KEY", "key_file": b.keyFile, "usage": "opencode",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		// The invocation a real harness needs: its subcommand, the model this description
		// names, and the prompt FILE last (D1, 2026-09-11).
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		"board":        "mas-bandwidth/schema#876",
	}
	raw, _ := json.MarshalIndent(desc, "", "  ")
	write(t, b.worker, string(raw))
	return b
}

func build(t *testing.T, into, name, pkg string) string {
	t.Helper()
	bin := filepath.Join(into, name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", pkg, err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// swarm runs the built binary, which is what a stranger meets at a shell prompt.
func (b *bench) swarm(args ...string) (exit int, stdout, stderr string) {
	b.t.Helper()
	cmd := exec.Command(b.binary, args...)
	cmd.Dir = b.dir
	cmd.Env = []string{"PATH=" + b.path, "HOME=" + b.dir}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		b.t.Fatalf("running nova-swarm %s: %v", strings.Join(args, " "), err)
	}
	return exit, out.String(), errb.String()
}

func (b *bench) add(task string, extra ...string) string {
	b.t.Helper()
	file := filepath.Join(b.dir, fmt.Sprintf("task-%d.md", time.Now().UnixNano()))
	write(b.t, file, task)
	args := append([]string{"add", "--pool", b.pool, "--task", file, "--files", "5", "--tokens", "100000"}, extra...)
	exit, stdout, stderr := b.swarm(args...)
	if exit != 0 {
		b.t.Fatalf("add exited %d: %s%s", exit, stdout, stderr)
	}
	return field(b.t, stdout, "id=")
}

func (b *bench) run(args ...string) (int, string, string) {
	b.t.Helper()
	all := append([]string{"run", "--pool", b.pool, "--workers", "1", "--hours", "0.25", "--worker", b.worker}, args...)
	return b.swarm(all...)
}

func field(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, key) {
				return strings.TrimPrefix(token, key)
			}
		}
	}
	t.Fatalf("no %s field in:\n%s", key, out)
	return ""
}

func mustContain(t *testing.T, what, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("%s does not contain %q:\n%s", what, want, body)
	}
}

// ---------------------------------------------------------------------------------------

// A whole pass: one task in, one worker out, and every number on the RUN DONE line the truth
// about the pool rather than about the output.
func TestADispatcherRunsAJobEndToEnd(t *testing.T) {
	b := newBench(t)
	id := b.add("read this pull request against the rules\nFAKE-FINDINGS 2\nFAKE-USAGE 100 50 - - -\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d, want 0 (a pass that started, finished and drained)\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN POOL workers=1")
	mustContain(t, "the run", stdout, "RUN START id="+id)
	mustContain(t, "the run", stdout, "result=ok findings=2 refusals=0")
	mustContain(t, "the run", stdout, "dest=done")
	mustContain(t, "the run", stdout, "RUN OK started=1 done=1 failed=0 killed=0 pending=0")

	// Rule 12: the usage file is outside everything reclaim removes, and it carries the
	// sixteen columns in order.
	usage := filepath.Join(b.pool, "usage", id+".tsv")
	raw, err := os.ReadFile(usage)
	if err != nil {
		t.Fatalf("no usage file at %s: %v", usage, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the usage file wants one header line and one row, got %d lines:\n%s", len(lines), raw)
	}
	head := strings.Split(lines[0], "\t")
	want := []string{"job", "attempt", "from", "started", "ended", "end", "rc", "provider", "model", "repo",
		"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd"}
	if len(head) != len(want) {
		t.Fatalf("the usage file has %d columns, want %d: %v", len(head), len(want), head)
	}
	for i := range want {
		if head[i] != want[i] {
			t.Errorf("usage column %d is %q, want %q", i, head[i], want[i])
		}
	}
	row := strings.Split(lines[1], "\t")
	if row[5] != "done" || row[10] != "100" || row[11] != "50" || row[12] != "-" {
		t.Errorf("the usage row wants end=done tokens_in=100 tokens_out=50 cache_write=-, got %v", row)
	}

	// Rule 6: the key is in no file under the pool and in no line this tool printed.
	if strings.Contains(stdout+stderr, fakeKey) {
		t.Error("the key reached an event line")
	}
	if found := grepTree(t, b.pool, fakeKey); found != "" {
		t.Errorf("the key is at rest in a file under the pool: %s", found)
	}
	if found := grepTree(t, filepath.Join(b.dir, "worker-home-1"), fakeKey); found != "" {
		t.Errorf("the key is at rest in a file under the slot: %s", found)
	}
	cfg, err := os.ReadFile(filepath.Join(b.dir, "worker-home-1", "opencode.json"))
	if err != nil {
		t.Fatalf("the harness config was not written: %v", err)
	}
	mustContain(t, "the harness config", string(cfg), "{env:FAKE_KEY}")

	// Rule 14: one line down.
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage", stdout, "TRIAGE BATCH batch=- reports=1 findings=2 new=2 dup=0 unquoted=0")
	mustContain(t, "triage", stdout, "accurate=- wrong=-")

	// Rule 15: the one path from a report to a person, verbatim.
	exit, stdout, _ = b.swarm("result", "--pool", b.pool, "--id", id)
	if exit != 0 {
		t.Fatalf("result exited %d", exit)
	}
	mustContain(t, "result", stdout, "RESULT OK id="+id)
	mustContain(t, "result", stdout, "## Head")

	// Rule 12 again: reclaim removes the job directory only with both the usage file and
	// the report copy, and cost answers after the directory is gone.
	exit, stdout, stderr = b.swarm("reclaim", "--pool", b.pool, "--task", id)
	if exit != 0 {
		t.Fatalf("reclaim exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "reclaim", stdout, "RECLAIM OK id="+id)
	exit, stdout, _ = b.swarm("cost", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("cost exited %d", exit)
	}
	mustContain(t, "cost", stdout, "COST OK tasks=1 in=100 out=50")
	mustContain(t, "cost", stdout, "dashes=0,0,1,1,1")
}

// Rule 8: completion is evidence, separate from the count. A report with no head is
// plan-only and lands in failed/, however much else it holds; a finished review with
// findings: 0 is CLEAN and lands in done/, because a tool that failed it would be paying a
// worker for finding something.
func TestCompletionIsEvidenceNotCount(t *testing.T) {
	b := newBench(t)
	clean := b.add("a bounded review that finds nothing\nFAKE-FINDINGS 0\n")
	plan := b.add("a run that ends on a refusal\nFAKE-NOHEAD\nFAKE-REFUSE 2\n")

	exit, stdout, stderr := b.run("--workers", "2")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "result=clean findings=0")
	mustContain(t, "the run", stdout, "result=plan-only findings=0 refusals=2")
	if !strings.Contains(stdout, "id="+clean) || !strings.Contains(stdout, "id="+plan) {
		t.Fatalf("both jobs want a RUN line:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "done", clean+".task")); err != nil {
		t.Errorf("a clean report belongs in done/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "failed", plan+".task")); err != nil {
		t.Errorf("a plan-only report belongs in failed/: %v", err)
	}
	exit, stdout, _ = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d", exit)
	}
	mustContain(t, "triage", stdout, "clean=1 plan_only=1")
}

// Rule 15: a malformed report is QUARANTINED -- never folded, no finding of it counted,
// whatever its head says -- and `result --id` is the one path to a person.
func TestResultShapeIsMechanical(t *testing.T) {
	b := newBench(t)
	id := b.add("a report with a fourth state word\nFAKE-FINDINGS 2\nFAKE-MALFORMED\n")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN MALFORMED id="+id)
	mustContain(t, "the run", stdout, "dest=failed")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".task")); err != nil {
		t.Errorf("a malformed report's job belongs in failed/: %v", err)
	}
	exit, stdout, _ = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d", exit)
	}
	mustContain(t, "triage", stdout, "TRIAGE QUARANTINED id="+id)
	mustContain(t, "triage", stdout, "malformed=1")
	if strings.Contains(stdout, "TRIAGE FINDING") {
		t.Error("a malformed report's findings must never be folded, valid or not")
	}
	page := field(t, stdout, "page=")
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), id) {
		t.Error("no line of a malformed report may reach the page")
	}
	exit, stdout, _ = b.swarm("result", "--pool", b.pool, "--id", id)
	if exit != 0 {
		t.Fatalf("result exited %d", exit)
	}
	mustContain(t, "result", stdout, "class=malformed")
	mustContain(t, "result", stdout, "| the item as it was handed to me | probably |")
}

// Rule 7: a job reaped at its deadline is re-queued ONCE, marked, and a second reap fails
// it. The remedy line names requeue with a smaller budget, which is a person's act.
func TestAReapedJobRunsOnceMore(t *testing.T) {
	if testing.Short() {
		t.Skip("this one waits for two deadlines")
	}
	b := newBench(t)
	id := b.add("a worker that sleeps past its deadline\nFAKE-SLEEP 30\n", "--deadline", "1s")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN KILLED id="+id)
	mustContain(t, "the run", stdout, "requeued=true reaped=1")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".task")); err != nil {
		t.Errorf("a reaped job's files belong in failed/: %v", err)
	}
	// The re-queued task is a NEW job id carrying from=<old-id>, and the same pass picks it
	// up, reaps it a second time, and does not queue a third: one automatic retry closes the
	// case where a worker was silent because the provider was, and never the case where the
	// task was too big, which a second identical run would only prove twice.
	mustContain(t, "the run", stdout, "requeued=false reaped=2")
	failed, err := os.ReadDir(filepath.Join(b.pool, "failed"))
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 4 { // two tasks, each a .task and a .json
		t.Errorf("failed/ wants the two attempts and nothing else, got %d files", len(failed))
	}
	pending, err := os.ReadDir(filepath.Join(b.pool, "pending"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("a job reaped twice is not re-queued again, got %d files in pending/", len(pending))
	}
	mustContain(t, "the run", stdout, "RUN OK started=2 done=0 failed=0 killed=2 pending=0")
	mustContain(t, "the remedy line", stdout, "RUN NOTE a worker was killed at its deadline twice")
}

// Rule 10: the note file, appended by the tool, counted in the report.
func TestANoteReachesARunningWorker(t *testing.T) {
	b := newBench(t)
	id := b.add("a worker that reads its notes\nFAKE-SLEEP 2\nFAKE-FINDINGS 1\n")
	go func() {
		// The note lands while the job is running, which is the whole point of the file.
		for i := 0; i < 100; i++ {
			if exit, _, _ := b.swarm("note", "--pool", b.pool, "--task", id, "--text", "look at the owed list first"); exit == 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "notes=1/1")
}

// Rule 13, the rate-limit half: a provider's 429 is not a failed task. The dispatcher
// holds the slot, waits the backoff, and retries the SAME task once; a second 429 fails it
// with rc=429 in its sidecar and its cost row.
func TestA429IsRetriedOnceAndThenFailed(t *testing.T) {
	b := newBench(t)
	id := b.add("a provider that is rate limited\nFAKE-429\nFAKE-LAUNCHES\nFAKE-USAGE 10 5 - - -\n")

	exit, stdout, stderr := b.run("--backoff", "1")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	// Retried once and no third time: two attempts, each a RUN DONE.
	if n := strings.Count(stdout, "RUN DONE id="); n != 2 {
		t.Fatalf("a 429 wants exactly two attempts (one retry), got %d:\n%s", n, stdout)
	}
	mustContain(t, "the retry", stdout, "rc=429")
	mustContain(t, "the retry", stdout, "dest=failed")

	// The retry is a new attempt carrying from=<first>; its sidecar and its cost row both
	// carry rc=429.
	var retry string
	entries, err := os.ReadDir(filepath.Join(b.pool, "failed"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(b.pool, "failed", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var sc struct {
			RC   int    `json:"rc"`
			From string `json:"from"`
		}
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatal(err)
		}
		if sc.From == id && sc.RC == 429 {
			retry = strings.TrimSuffix(e.Name(), ".json")
		}
	}
	if retry == "" {
		t.Fatalf("the retry wants a sidecar with from=%s and rc=429 under failed/", id)
	}
	row, err := os.ReadFile(filepath.Join(b.pool, "usage", retry+".tsv"))
	if err != nil {
		t.Fatalf("the retry's cost row is missing: %v", err)
	}
	if !strings.Contains(string(row), "\t429\t") {
		t.Errorf("the retry's cost row wants rc=429:\n%s", row)
	}
}

// A retry is a second attempt with its own usage row, and `cost` sums each attempt once:
// two rows of 10 in and 5 out are 20 and 10, never 40 and 20.
func TestTwoAttemptsSumOnceEach(t *testing.T) {
	b := newBench(t)
	b.add("a provider that is rate limited\nFAKE-429\nFAKE-USAGE 10 5 - - -\n")

	exit, stdout, stderr := b.run("--backoff", "1")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	exit, stdout, stderr = b.swarm("cost", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("cost exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "cost", stdout, "COST OK tasks=2 in=20 out=10")
	if n := strings.Count(stdout, "COST TASK "); n != 2 {
		t.Errorf("two attempts want two COST TASK rows, got %d:\n%s", n, stdout)
	}
}

// grepTree reports the first file under dir holding needle, or "".
func grepTree(t *testing.T, dir, needle string) string {
	t.Helper()
	found := ""
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err == nil && bytes.Contains(raw, []byte(needle)) {
			found = path
		}
		return nil
	})
	return found
}

// D1 (the real run, 2026-09-11): `model` was decoded, required, printed -- and never
// reached the child. `opencode <path>/PROMPT.md` reads the path as a PROJECT DIRECTORY,
// so both jobs died in two seconds under a green RUN OK. The worker description's
// harness_args carry the invocation, `{model}` is where the model goes, and `{prompt}` is
// where the prompt file goes; the fake harness refuses an invocation a real one would not
// understand, so the contract the tests run is the contract a first run meets.
func TestTheHarnessIsToldWhichModelToRun(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a task that proves the invocation reached the child\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	argv := b.jobFile(id, "argv")
	if !strings.Contains(argv, "--model fake-model") {
		t.Errorf("the child's argv wants `--model fake-model`, the model the description names:\n%s", argv)
	}
	if !strings.HasPrefix(argv, "run ") {
		t.Errorf("the child's argv wants the harness's own subcommand first, from harness_args:\n%s", argv)
	}
	if !strings.HasSuffix(strings.TrimSpace(argv), "PROMPT.md") {
		t.Errorf("the prompt FILE is the last argument, never the task text:\n%s", argv)
	}
}

// A worker description that never places the model is refused BEFORE any worker starts,
// naming the field and showing the shape. This is D1 caught at the door.
func TestAWorkerDescriptionWithoutTheModelIsRefused(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.add("a task nothing will run\n")
	b.rewriteWorker(func(d map[string]any) { delete(d, "harness_args") })

	exit, stdout, stderr := b.run()
	if exit != 2 {
		t.Fatalf("a description with no harness_args exits %d, want 2:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "harness_args")
	mustContain(t, "the refusal", stderr, "{model}")
	if strings.Contains(stdout, "RUN START") {
		t.Errorf("the refusal comes before any worker starts:\n%s", stdout)
	}
}

// jobFile reads one file the child wrote under its job directory, found by the job id.
func (b *bench) jobFile(id, name string) string {
	b.t.Helper()
	var found string
	root := filepath.Join(b.dir, "worker-home-1", "jobs", id)
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		b.t.Fatalf("no %s under %s: %v", name, root, err)
	}
	found = string(raw)
	return found
}

// rewriteWorker edits the worker description in place, so a test can take one field away.
func (b *bench) rewriteWorker(edit func(map[string]any)) {
	b.t.Helper()
	raw, err := os.ReadFile(b.worker)
	if err != nil {
		b.t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		b.t.Fatal(err)
	}
	edit(d)
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		b.t.Fatal(err)
	}
	write(b.t, b.worker, string(out))
}

// DEMANDED TEST 11 (SPEC-SWARM.md:1274). A fake worker that forks a process which outlives
// it ends `RUN VIOLATION background=1`, the survivor is dead afterwards, the job is in
// `failed/` with `violation=background`, and `TRIAGE BATCH` DOES NOT COUNT ITS REPORT
// (SPEC-SWARM.md:156, "and `triage` does not count it"). A worker that forks and WAITS for
// its child is not a violation. The prompt carries the one-process sentence. Every job
// prints exactly one of `RUN DONE` or `RUN VIOLATION`. And SPEC-SWARM.md:541: a run that
// ended with a quarantined slot exits 1.
func TestABackgroundedChildIsAViolationAndIsNotTriaged(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker that leaves a process behind\nFAKE-FINDINGS 2\nFAKE-BACKGROUND\n")

	exit, stdout, stderr := b.run()
	// A run that quarantined a slot says NO.
	if exit != 1 {
		t.Errorf("a run that quarantined a slot exits %d, want 1 (SPEC-SWARM.md:541):\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN VIOLATION id="+id)
	mustContain(t, "the run", stdout, "background=1")
	if strings.Contains(stdout, "RUN DONE id="+id) {
		t.Errorf("a job reports EXACTLY ONCE: one RUN DONE or one RUN VIOLATION, never both:\n%s", stdout)
	}

	// The job is in failed/ with violation=background.
	sc := b.sidecar(id)
	if sc.Violation != "background" {
		t.Errorf("the sidecar wants violation=background, got %q", sc.Violation)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".json")); err != nil {
		t.Errorf("a violation belongs in failed/: %v", err)
	}

	// THE RULE THIS TEST IS FOR: triage does not count a quarantined result.
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage", stdout, "reports=0")
	if strings.Contains(stdout, id) {
		t.Errorf("triage folded a quarantined result into a coordinator's page:\n%s", stdout)
	}
	if strings.Contains(stdout, "TRIAGE FINDING") {
		t.Errorf("a violation's findings are not a batch's findings:\n%s", stdout)
	}

	// The prompt says it in one sentence.
	prompt := b.jobFile(id, "PROMPT.md")
	mustContain(t, "the prompt", prompt, "THIS JOB IS ONE PROCESS")
	mustContain(t, "the prompt", prompt, "two independent things is two tasks")
}

// The other half of rule 11: a worker that forks and WAITS for its child is not a
// violation. Nothing survives it, so nothing is quarantined and the run exits 0.
func TestAWorkerThatWaitsForItsChildIsNotAViolation(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker that forks and waits\nFAKE-FINDINGS 1\nFAKE-FOREGROUND-CHILD\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("a worker that waits for its child is no violation; exit %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE id="+id)
	if strings.Contains(stdout, "RUN VIOLATION") {
		t.Errorf("waiting for your own child is not backgrounding it:\n%s", stdout)
	}
	mustContain(t, "the run", stdout, "dest=done")
}

// sidecar reads one job's sidecar wherever it is in the pool.
func (b *bench) sidecar(id string) swarmSidecar {
	b.t.Helper()
	for _, state := range []string{"pending", "running", "done", "failed"} {
		raw, err := os.ReadFile(filepath.Join(b.pool, state, id+".json"))
		if err != nil {
			continue
		}
		var sc swarmSidecar
		if err := json.Unmarshal(raw, &sc); err != nil {
			b.t.Fatal(err)
		}
		return sc
	}
	b.t.Fatalf("no sidecar for %s anywhere in %s", id, b.pool)
	return swarmSidecar{}
}

// swarmSidecar is the handful of sidecar fields these tests read. It is deliberately its
// own type: a test that imported the package's struct would pass when the FILE stopped
// carrying a field the struct still has.
type swarmSidecar struct {
	ID        string `json:"id"`
	Violation string `json:"violation,omitempty"`
	Launch    string `json:"launch,omitempty"`
	End       string `json:"end,omitempty"`
	RC        int    `json:"rc"`
	Class     string `json:"class,omitempty"`
}

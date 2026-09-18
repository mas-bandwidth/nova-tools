package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// THE CONTRACT TESTS. Every one of them runs against the FAKE HARNESS binary on PATH,
// inside t.TempDir(), with no network and no provider -- so the dispatcher is tested end to
// end and nothing here costs a token or reaches a key that is worth anything.
//
// CONTRIBUTING.md: test code is code. No test here reaches outside t.TempDir(), none
// touches the network, and none matches a process by its command line.

// bench is one pool, one worker description, one key file and a fake harness on PATH.
type bench struct {
	t        *testing.T
	dir      string
	pool     string
	binary   string
	worker   string
	keyFile  string
	path     string
	extraEnv []string
	// THE LAUNCH SEAM (docs/SPEC-SANDBOX.md, "the two callers"): every job runs inside
	// nova-sandbox, so every bench knows where that binary is. `sandbox` is the REAL one,
	// built from this repository, which has a wall on darwin and refuses elsewhere;
	// `fakeSandbox` is the stand-in that records its argv and enforces nothing, so the
	// seam itself is testable on a platform whose body is not built.
	sandbox     string
	fakeSandbox string
}

const fakeKey = "sk-fake-0123456789-not-a-key"

func newBench(t *testing.T) *bench {
	t.Helper()
	dir := t.TempDir()
	b := &bench{t: t, dir: dir, pool: filepath.Join(dir, "pool")}
	if err := os.MkdirAll(b.pool, 0o755); err != nil {
		t.Fatal(err)
	}
	// The three binaries are built ONCE for the whole package, not once per bench. Thirty
	// benches building them each saturated the machine, and a dispatcher that cannot start
	// a child inside its deadline turns a contract test into a race: three tests that kill
	// a worker went red under the load and green on their own (2026-09-11).
	b.binary, b.path = builtBinaries(t)
	b.sandbox, b.fakeSandbox = builtSandbox, builtFakeSandbox

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

// builtBinaries hands every bench the two files the package builds once: the tool's own
// path, and a PATH whose first entry holds the fake harness. The build itself happens in
// buildShared, which TestMain runs before any test, so no test's own elapsed time carries
// the compile (the studio bench charged it to TestBenchProbeOK).
func builtBinaries(t *testing.T) (string, string) {
	t.Helper()
	if err := buildShared(); err != nil {
		t.Fatalf("building the binaries these tests run: %v", err)
	}
	return builtTool, builtPath
}

// buildShared builds every binary and fixture the whole package shares: the two nova-swarm
// builds, the fake harness, the fake sqlite3, the real sandbox and its stand-in, and a
// PATH directory naming the stand-in `nova-sandbox`. It runs once, from TestMain, so the
// compile is charged to the package and never to whichever test happens to ask first.
func buildShared() error {
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-swarm-binaries")
		if err != nil {
			buildErr = err
			return
		}
		builtDir = dir
		builtTool, buildErr = build(dir, "nova-swarm", "./cmd/nova-swarm")
		if buildErr != nil {
			return
		}
		// THE TAGGED BUILD. The same binary with -tags swarmtest, which is the only build
		// that honours the NOVA_SWARM_* injection variables. The recovery tests that plant
		// a kill or a pause run this one; every other test runs the release build above.
		if builtTaggedTool, buildErr = buildTagged(dir, "nova-swarm-swarmtest", "./cmd/nova-swarm"); buildErr != nil {
			return
		}
		harnessDir := filepath.Join(dir, "bin")
		if buildErr = os.MkdirAll(harnessDir, 0o755); buildErr != nil {
			return
		}
		builtHarness, buildErr = build(harnessDir, "fake-harness", "./cmd/nova-swarm/testdata/fakeharness")
		if buildErr != nil {
			return
		}
		// The one program the usage source runs. It is a stand-in, on the same PATH as the
		// fake harness, so the dispatcher reads a database end to end with no sqlite3 of
		// the machine's and no provider (SPEC-SWARM rule 12).
		if _, buildErr = build(harnessDir, "sqlite3", "./internal/swarm/testdata/fakesqlite"); buildErr != nil {
			return
		}
		// THE WALL AND THE STAND-IN. nova-sandbox is the binary every job now runs inside
		// (docs/SPEC-SANDBOX.md); it is built here, from this repository, so that no test
		// depends on what is installed on the machine. The fake beside it records the argv
		// the dispatcher built and enforces nothing, which is how the seam is tested on a
		// platform whose sandbox body is not built.
		if builtSandbox, buildErr = build(harnessDir, "nova-sandbox", "./cmd/nova-sandbox"); buildErr != nil {
			return
		}
		if builtFakeSandbox, buildErr = build(harnessDir, "fake-sandbox", "./cmd/nova-swarm/testdata/fakesandbox"); buildErr != nil {
			return
		}
		// THE STAND-IN ON PATH. nativeSandboxOnPath resolves `nova-sandbox` through PATH
		// rather than a --sandbox flag, and the real sandbox of the build above already
		// owns that name in harnessDir; a link under its own name in a second directory
		// keeps both without a per-test compile.
		pathBin := filepath.Join(dir, "pathbin")
		if buildErr = os.MkdirAll(pathBin, 0o755); buildErr != nil {
			return
		}
		if buildErr = linkExecutable(builtFakeSandbox, filepath.Join(pathBin, "nova-sandbox"+exeSuffix())); buildErr != nil {
			return
		}
		builtPathBin = pathBin
		builtPath = harnessDir + string(os.PathListSeparator) + os.Getenv("PATH")
	})
	return buildErr
}

var (
	buildOnce        sync.Once
	builtDir         string
	builtTool        string
	builtTaggedTool  string
	builtPath        string
	builtPathBin     string
	builtHarness     string
	builtSandbox     string
	builtFakeSandbox string
	buildErr         error
)

// TestMain builds the one set of binaries the whole package shares before any test runs,
// and removes the one directory they live in after the last. The compile is charged here
// and never to whichever test asks first.
func TestMain(m *testing.M) {
	if err := buildShared(); err != nil {
		fmt.Fprintf(os.Stderr, "building the binaries these tests run: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if builtDir != "" {
		_ = os.RemoveAll(builtDir)
	}
	os.Exit(code)
}

// exeSuffix is the name a Windows binary carries and a unix one does not.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// linkExecutable places src under name in the same tree. A hard link needs no privilege and
// leaves one inode; a symlink is the fallback where the filesystem refuses a link.
func linkExecutable(src, name string) error {
	if err := os.Link(src, name); err == nil {
		return nil
	}
	return os.Symlink(src, name)
}

func build(into, name, pkg string) (string, error) {
	return buildWith(into, name, pkg)
}

// buildTagged builds with -tags swarmtest, the build whose injection functions read the
// NOVA_SWARM_* environment variables. It is the binary the recovery tests run.
func buildTagged(into, name, pkg string) (string, error) {
	return buildWith(into, name, pkg, "swarmtest")
}

func buildWith(into, name, pkg string, tags ...string) (string, error) {
	bin := filepath.Join(into, name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"build", "-o", bin}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, pkg)
	root, err := repoRootPath()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = goenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s: %v\n%s", pkg, err, out)
	}
	return bin, nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRootPath()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// repoRootPath is the repository root from the package directory every test runs in.
func repoRootPath() (string, error) {
	return filepath.Abs(filepath.Join("..", ".."))
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

// swarm runs the built binary, which is what a stranger meets at a shell prompt. It fails
// the test on a binary that would not run at all, so it belongs to the TEST GOROUTINE:
// anything running beside the test calls swarmTry and hands the answer back.
func (b *bench) swarm(args ...string) (exit int, stdout, stderr string) {
	b.t.Helper()
	exit, stdout, stderr, err := b.swarmTry(args...)
	if err != nil {
		b.t.Fatalf("running nova-swarm %s: %v", strings.Join(args, " "), err)
	}
	return exit, stdout, stderr
}

// inject switches this bench to the swarmtest build, the one whose injection functions read
// the NOVA_SWARM_* environment variables. A test that plants a kill or a pause calls it;
// every other test runs the release build, which ignores those variables entirely.
func (b *bench) inject() {
	b.t.Helper()
	b.binary = builtTaggedTool
}

// swarmTry is swarm with the failure RETURNED rather than reported: t.Fatalf from a
// goroutine other than the test's own ends that goroutine and not the test, and after
// t.TempDir has been cleaned it panics (#122). Every caller off the test goroutine uses
// this one and the test goroutine does the asserting.
func (b *bench) swarmTry(args ...string) (exit int, stdout, stderr string, err error) {
	cmd := exec.Command(b.binary, args...)
	cmd.Dir = b.dir
	cmd.Env = append([]string{"PATH=" + b.path, "Path=" + b.path, "HOME=" + b.dir}, b.extraEnv...)
	if runtime.GOOS == "windows" {
		for _, k := range []string{"SystemRoot", "SYSTEMROOT", "SystemDrive", "PATHEXT", "TEMP", "TMP", "COMSPEC"} {
			if v := os.Getenv(k); v != "" {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	switch runErr := cmd.Run(); {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		exit = exitErr.ExitCode()
	default:
		err = runErr
	}
	return exit, out.String(), errb.String(), err
}

// runWatching runs the dispatcher and hands each line of its stdout to watch as it is
// printed, so a test can act on a RUN line (an adoption) before the run ends. It returns
// the run's exit code and its full stdout and stderr. It carries the same environment
// swarmTry builds, so it is compiled and usable on every platform.
func (b *bench) runWatching(args []string, watch func(string)) (int, string, string) {
	b.t.Helper()
	cmd := exec.Command(b.binary, args...)
	cmd.Dir = b.dir
	cmd.Env = append([]string{"PATH=" + b.path, "Path=" + b.path, "HOME=" + b.dir}, b.extraEnv...)
	if runtime.GOOS == "windows" {
		for _, k := range []string{"SystemRoot", "SYSTEMROOT", "SystemDrive", "PATHEXT", "TEMP", "TMP", "COMSPEC"} {
			if v := os.Getenv(k); v != "" {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		b.t.Fatalf("opening the run's stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		b.t.Fatalf("starting the run: %v", err)
	}
	var out bytes.Buffer
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		out.WriteString(line)
		out.WriteString("\n")
		if watch != nil {
			watch(line)
		}
	}
	_ = cmd.Wait()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

func (b *bench) add(task string, extra ...string) string {
	b.t.Helper()
	// THE FIXTURE'S OWN CAP, ENFORCED HERE (#132; fixture_guards_test.go carries the why):
	// a hold -- `FAKE-SLEEP` plus `FAKE-AWAIT-NOTE`, in that order, as the fake runs them --
	// that reaches the deadline this job will run under is refused before the job is queued,
	// because the wait and the reaper would come due together and the silent `end=killed` of
	// #126 would be back. The deadline READ HERE is the one standing at add time; `b.run`
	// applies the same cap against the description the run hands the dispatcher, which is
	// what covers a rewrite after the add and the verbs that skip `add`.
	deadline, err := benchJobDeadline(b.worker, extra)
	if err != nil {
		b.t.Fatalf("reading the deadline this job would run under: %v", err)
	}
	if err := awaitNoteCap(task, deadline); err != nil {
		b.t.Fatalf("this fixture would recreate the silent kill of #126: %v", err)
	}
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
	// THE CAP AGAIN, WHERE THE JOB'S DEADLINE IS ACTUALLY RESOLVED (#132, the #140 read's
	// finding 2): every task still pending, against the description THIS run hands the
	// dispatcher -- so a deadline rewritten after the add, another `--worker`, and the jobs
	// `batch` and `requeue --task-file` queue without going through `add` are all covered.
	// fixture_guards_test.go names what stays uncovered.
	worker := b.worker
	if named, ok := flagAfter(all, "--worker"); ok {
		worker = named
	}
	if err := capPendingTasks(b.pool, worker); err != nil {
		b.t.Fatalf("this fixture would recreate the silent kill of #126: %v", err)
	}
	return b.swarm(withSandbox(all)...)
}

// withSandbox is how every `run` in this package names the wall, and it is one function
// rather than a flag typed thirty times: on darwin, whose body is built, the contract tests
// run INSIDE the real nova-sandbox, which is the whole point of the seam -- the transaction
// is proved where it actually runs. On a platform whose body is not built, nova-sandbox
// REFUSES (rule 1), so the tests take rule 11's one loud workaround and say so in the argv
// where a reader can see it. A caller that already named one is left alone.
func withSandbox(args []string) []string {
	for _, a := range args {
		if a == "--sandbox" || a == "--no-sandbox" {
			return args
		}
	}
	if runtime.GOOS == "darwin" {
		return append(args, "--sandbox", builtSandbox)
	}
	return append(args, "--no-sandbox")
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
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	id := b.add("read this pull request against the rules\nFAKE-FINDINGS 2\nFAKE-USAGE 100 50 - - -\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d, want 0 (a pass that started, finished and drained)\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN POOL workers=1")
	mustContain(t, "the run", stdout, "auto_retry=true")
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

// DEMANDED TEST 8 (SPEC-SWARM.md:1253). EIGHT JOBS, and the counts they print.
//
// Completion is EVIDENCE, separate from the count: a report with no head is plan-only and
// lands in failed/ however much else it holds; a finished review with `findings: 0` is
// CLEAN and lands in done/, because a tool that failed it would be paying a worker for
// finding something.
//
// The eight the spec names: three with findings, one completed review with `findings: 0`
// and no finding lines, one completed probe-row with `findings: 0` and one item `not done`
// with its reason, one report with a plan and no head, one with no head and one finding
// line appended before the kill, and one with no RESULT.md. (The kill itself is demanded
// test 3's; what decides these counts is the SHAPE that kill leaves -- a headless report
// carrying the finding line the worker had appended by then.)
func TestCompletionIsEvidenceNotCount(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	found1 := b.add("a review that found two things\nFAKE-FINDINGS 2\n")
	found2 := b.add("another review that found two things\nFAKE-FINDINGS 2\n")
	found3 := b.add("a third review that found two things\nFAKE-FINDINGS 2\n")
	clean := b.add("a bounded review that finds nothing\nFAKE-FINDINGS 0\n")
	probe := b.add("a probe row that finished not done\nFAKE-FINDINGS 0\nFAKE-NOTDONE\n", "--template", "probe-row")
	plan := b.add("a run that ends on a refusal with no head\nFAKE-NOHEAD\nFAKE-REFUSE 2\n")
	headless := b.add("a worker that appended one finding and never wrote its head\nFAKE-NOHEAD\nFAKE-FINDINGS 1\n")
	noResult := b.add("a worker that published nothing at all\nFAKE-NORESULT\n")

	exit, stdout, stderr := b.run("--workers", "8")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	for _, id := range []string{found1, found2, found3, clean, probe, plan, headless, noResult} {
		if !strings.Contains(stdout, "id="+id) {
			t.Fatalf("every job wants a RUN line, %s has none:\n%s", id, stdout)
		}
	}
	mustContain(t, "the run", stdout, "result=clean findings=0")
	mustContain(t, "the run", stdout, "result=plan-only findings=0 refusals=2")
	mustContain(t, "the run", stdout, "result=no-result")
	// The two clean reports are in done/ and RUN OK counts them done; the two plan-only
	// are in failed/, and so is the job that published nothing.
	mustContain(t, "the run", stdout, "RUN OK started=8 done=5 failed=3 killed=0 pending=0")
	for _, id := range []string{clean, probe} {
		if _, err := os.Stat(filepath.Join(b.pool, "done", id+".task")); err != nil {
			t.Errorf("a clean report belongs in done/: %v", err)
		}
	}
	for _, id := range []string{plan, headless, noResult} {
		if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".task")); err != nil {
			t.Errorf("%s belongs in failed/: %v", id, err)
		}
	}

	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	// ONE LINE, and `reports=` is the number of jobs that HAVE a report to read -- seven of
	// the eight. `findings=` counts every finding line once, the headless report's appended
	// one among them: 3x2 + 1. Three of the six fold under one repo and rev, which moves
	// them from `new` to `dup` and changes neither `findings` nor the classification.
	if n := strings.Count(stdout, "TRIAGE BATCH "); n != 1 {
		t.Fatalf("the batch line prints exactly once, got %d:\n%s", n, stdout)
	}
	mustContain(t, "triage", stdout, "reports=7 findings=7 new=3 dup=4 unquoted=0 clean=2 plan_only=2 no_result=1 malformed=0")
	// With no verdict recorded, both numbers are a dash: an absence is never a zero.
	mustContain(t, "triage", stdout, "accurate=- wrong=-")

	// The probe row's `not done` item carries its reason, and it is counted as not done.
	mustContain(t, "triage", stdout, "notdone=1")

	// One recorded verdict, and the numbers are its numbers.
	if exit, stdout, stderr = b.swarm("verdict", "--pool", b.pool, "--task", found1, "--who", "rowan", "--accurate", "2", "--wrong", "1"); exit != 0 {
		t.Fatalf("verdict exited %d: %s%s", exit, stdout, stderr)
	}
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool, "--all")
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage after a verdict", stdout, "accurate=2 wrong=1")
	mustContain(t, "triage after a verdict", stdout, "reports=7 findings=7")
}

// Rule 15: a malformed report is QUARANTINED -- never folded, no finding of it counted,
// whatever its head says -- and `result --id` is the one path to a person.
func TestResultShapeIsMechanical(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
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

func TestNoAutoRetryHelpStatesItsScopeAndExistingRunControls(t *testing.T) {
	b := newBench(t)
	exit, stdout, stderr := b.swarm("help")
	if exit != 0 {
		t.Fatalf("help exited %d: %s%s", exit, stdout, stderr)
	}
	for _, want := range []string{
		"[--no-auto-retry]", "--no-auto-retry is run-only", "a later recovery run needs the flag again",
		"--max, default 20, 0 for all", "it never limits\nstarts, workers, attempts or retries",
		"stop stops new admissions and drains workers already running",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not explain %q:\n%s", want, stdout)
		}
	}
}

// Rule 7: a job reaped at its deadline is re-queued ONCE, marked, and a second reap fails
// it. The remedy line names requeue with a smaller budget, which is a person's act.
// An explicit one-attempt run keeps the killed attempt and its usage evidence, but
// cannot create a same-text descendant. The normal rule-7 test below remains the
// backwards-compatibility witness for automatic retries.
func TestNoAutoRetryKeepsAKilledAttemptWithoutADescendant(t *testing.T) {
	if testing.Short() {
		t.Skip("this one waits for the worker deadline")
	}
	b := newBench(t)
	id := b.add("a one-attempt worker that publishes before its deadline\nFAKE-PUBLISH-FIRST\nFAKE-FINDINGS 1\nFAKE-SLEEP 30\n", "--deadline", "800ms")
	exit, stdout, stderr := b.run("--no-auto-retry")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the start summary", lineWith(t, stdout, "RUN POOL"), "auto_retry=false")
	mustContain(t, "the terminal summary", lineWith(t, stdout, "RUN OK"), "auto_retry=false")
	mustContain(t, "the killed attempt", stdout, "RUN KILLED id="+id)
	mustContain(t, "the killed attempt", stdout, "requeued=false reaped=1")
	if n := strings.Count(stdout, "RUN START id="); n != 1 {
		t.Fatalf("one-attempt policy started %d workers:\n%s", n, stdout)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".task")); err != nil {
		t.Fatalf("the original task is not retained in failed/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "usage", id+".tsv")); err != nil {
		t.Fatalf("the original usage row is not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "reports", id, "RESULT.md")); err != nil {
		t.Fatalf("the published report is not retained: %v", err)
	}
	pending, err := os.ReadDir(filepath.Join(b.pool, "pending"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("one-attempt policy created a descendant: %v", mustReadDirNames(t, filepath.Join(b.pool, "pending")))
	}
}

// The name no-auto-retry includes the dispatcher's true-429 child, not only the
// deadline child. It must skip the backoff as well as the descendant.
func TestNoAutoRetryKeepsA429AttemptWithoutADescendant(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	id := b.add("a one-attempt provider that is rate limited\nFAKE-429\nFAKE-USAGE 10 5 - - -\n")
	exit, stdout, stderr := b.run("--no-auto-retry", "--backoff", "1")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	if n := strings.Count(stdout, "RUN START id="); n != 1 {
		t.Fatalf("one-attempt policy started %d 429 workers:\n%s", n, stdout)
	}
	mustContain(t, "the start summary", lineWith(t, stdout, "RUN POOL"), "auto_retry=false")
	mustContain(t, "the terminal summary", lineWith(t, stdout, "RUN OK"), "auto_retry=false")
	mustContain(t, "the 429 attempt", stdout, "RUN DONE id="+id)
	mustContain(t, "the 429 attempt", stdout, "rc=429")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".json")); err != nil {
		t.Fatalf("the 429 sidecar is not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "usage", id+".tsv")); err != nil {
		t.Fatalf("the 429 usage row is not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.pool, "reports", id, "NO-RESULT")); err != nil {
		t.Fatalf("the finalized no-result marker is not retained: %v", err)
	}
	if pending, err := os.ReadDir(filepath.Join(b.pool, "pending")); err != nil || len(pending) != 0 {
		t.Fatalf("one-attempt 429 created a pending descendant: entries=%v err=%v", pending, err)
	}
}

// mustReadDirNames is one directory listing, named, so a test reads a pool the way a person
// does and fails on the read rather than on a nil slice three lines later.
func mustReadDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Rule 10: the note file, appended by the tool, counted in the report.
func TestANoteReachesARunningWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	// 20s, UNDER THIS BENCH'S 30s DEADLINE, and the cap is this fixture's to set. A wait
	// bounded BY the deadline is not bounded at all: the wait and the reaper come due in the
	// same instant, so a note that never arrives fails as `end=killed` -- rule 7's re-queue
	// and a second reap -- and says nothing about the wait that caused it. Ten seconds under
	// it, the worker outlives its own wait, publishes its report, and the fake names the
	// wait it gave up on in one line of stderr that the harness log carries.
	id := b.add("a worker that reads its notes\nFAKE-AWAIT-NOTE 20\nFAKE-FINDINGS 1\n")
	// THE WORKER HOLDS FOR THE NOTE, AND NOTHING HERE IS A CLOCK. The fake waits for the
	// note file to carry a line (`FAKE-AWAIT-NOTE`, bounded by its own seconds, which this
	// fixture keeps under the job's deadline) instead of sleeping two seconds: with a sleep,
	// a sender delayed past it -- a loaded runner, or a 3s stall in front of the send, which
	// reproduces it as `NOTE REFUSED` at 5.16s -- posts to a job that has already ended.
	// Both sides now wait on an observable: the sender on the running record, the worker on
	// the note.
	//
	// THE NOTE GOROUTINE OWNS NOTHING IT CANNOT HAND BACK (#122). It waits for an
	// OBSERVABLE -- the running record carrying this job's directory, which `run` writes
	// before the handshake and which is exactly what `note` needs to exist -- sends ONE
	// note, and puts its exit and its stderr on a channel. It never calls t.Fatal: a
	// t.Fatal off the test goroutine ends only that goroutine, and after t.TempDir's
	// cleanup it panics, which is the panic #120 logged. The test goroutine JOINS it below
	// and does every assertion itself.
	type noteRun struct {
		exit   int
		stderr string
		err    error
	}
	sent := make(chan noteRun, 1)
	go func() {
		res := noteRun{exit: -1, err: errors.New("the job never published a running record with a job directory")}
		defer func() { sent <- res }()
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if !runningJobDir(b.pool, id) {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			exit, _, errOut, err := b.swarmTry("note", "--pool", b.pool, "--task", id, "--text", "look at the owed list first")
			res = noteRun{exit: exit, stderr: errOut, err: err}
			return
		}
	}()
	exit, stdout, stderr := b.run()
	note := <-sent
	if note.err != nil {
		t.Fatalf("the note could not be sent: %v", note.err)
	}
	if note.exit != 0 {
		t.Fatalf("the note exited %d: %s", note.exit, note.stderr)
	}
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "notes=1/1")
}

// A WAIT THAT GIVES UP IS BOUNDED UNDER THE DEADLINE AND SAYS SO.
//
// The other half of the fixture above, and cheap: no note is ever sent. A `FAKE-AWAIT-NOTE`
// bound EQUAL to the job's deadline would end this run as `end=killed` -- rule 7's re-queue
// and a second reap -- with nothing anywhere naming the wait that caused it, which is the
// silent failure both ratifying reads of #126 named. Bounded UNDER the deadline the worker
// outlives its own wait, publishes its report, and the run is `dest=done`; and the wait
// names itself in one line the harness log carries, so a person reading the log after a
// green run still learns that no note arrived.
func TestAWorkerThatWaitedForANoteThatNeverCameSaysSo(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	id := b.add("a worker that waits for a note nobody sends\nFAKE-AWAIT-NOTE 1\nFAKE-FINDINGS 1\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	// The wait ended on its own, well under the 30s deadline: the report was published.
	mustContain(t, "the run", stdout, "result=ok")
	mustContain(t, "the run", stdout, "dest=done")
	mustContain(t, "the harness log", b.jobFile(id, "harness.log"), "the wait gave up")
}

// runningJobDir is the observable the note goroutine waits on: <pool>/running/<id>.json
// with a `job` in it. `note` REFUSES a task that is not running with a job directory, so
// this is the same question the verb asks, asked before it is asked -- not a sleep.
func runningJobDir(pool, id string) bool {
	raw, err := os.ReadFile(filepath.Join(pool, "running", id+".json"))
	if err != nil {
		return false
	}
	var sc struct {
		Job string `json:"job"`
	}
	return json.Unmarshal(raw, &sc) == nil && sc.Job != ""
}

// Rule 13, the rate-limit half: a provider's 429 is not a failed task. The dispatcher
// holds the slot, waits the backoff, and retries the SAME task once; a second 429 fails it
// with rc=429 in its sidecar and its cost row.
func TestA429IsRetriedOnceAndThenFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
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
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
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
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
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

// ISSUE #881 (a), the run half: the worker description already pins the model, and the
// dispatcher launches exactly that one. The fake harness records the model it was handed;
// the test asserts the description's model is the ONLY --model the harness ever sees, so a
// future path that launched another model for a key authorized for one goes red here.
func TestRunLaunchesOnlyTheDescriptionModel(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker under a pinned model\nFAKE-FINDINGS 1\n")

	// --no-sandbox, the one loud workaround (rule 11): the model reaches the harness the
	// same way walled or not, and this test runs on a machine whose sandbox backend is
	// blocked. The model pin is about the harness argv, never about the wall.
	exit, stdout, stderr := b.run("--no-sandbox")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the start summary", lineWith(t, stdout, "RUN POOL"), "model=fake-model")
	argv := b.jobFile(id, "argv")
	fields := strings.Fields(argv)
	saw := false
	for i, a := range fields {
		if a == "--model" && i+1 < len(fields) {
			saw = true
			if fields[i+1] != "fake-model" {
				t.Errorf("the harness was handed model %q, want the description's fake-model:\n%s", fields[i+1], argv)
			}
		}
	}
	if !saw {
		t.Errorf("the harness's argv carries no --model at all:\n%s", argv)
	}
}

// A worker description that never places the model is refused BEFORE any worker starts,
// naming the field and showing the shape. This is D1 caught at the door.
func TestAWorkerDescriptionWithoutTheModelIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
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

// ISSUE #881: a worker description may carry "secret": "<ENV NAME>" instead of key_file,
// and the value is delivered by nova-secrets exec into run's own environment, never a
// file on disk (docs/SPEC-SWARM.md, the worker description; docs/SPEC-SECRETS.md, the
// second caller). run and supervise require that variable present and non-empty there,
// pass it to the harness by name, and the value is never written to any file, never
// printed, and never in a RUN or SUPERVISE line.
func TestARunWithASecretWorkerUsesTheEnvironmentAndLeaksNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	b.rewriteWorker(func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = "FAKE_SECRET"
	})
	id := b.add("a worker under a secret-delivered key\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	b.extraEnv = append(b.extraEnv, "FAKE_SECRET="+fakeKey)

	exit, stdout, stderr := b.run("--no-sandbox")
	if exit != 0 {
		t.Fatalf("run exited %d, want 0: the variable was present, so admission accepted it\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN START id="+id)
	mustContain(t, "the run", stdout, "dest=done")
	// The harness received the value by environment; the fake proves it by length, never
	// by value.
	mustContain(t, "the harness log", harnessLog(t, b, id), "the key is present, length")
	// The value is in no event line and in no file under the pool or the slot, and the
	// config the tool writes carries the variable's NAME, never the value.
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

	// The variable ABSENT from run's environment is refused, naming the variable and the
	// remedy, before any worker starts.
	b2 := newBench(t)
	b2.rewriteWorker(func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = "FAKE_SECRET"
	})
	b2.add("a worker whose secret was not delivered\nFAKE-FINDINGS 1\n")
	b2.extraEnv = append(b2.extraEnv, "FAKE_SECRET=")
	exit, stdout, stderr = b2.run("--no-sandbox")
	if exit != 2 {
		t.Fatalf("a secret absent from the environment exits %d, want 2:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "FAKE_SECRET")
	mustContain(t, "the refusal", stderr, "nova-secrets exec")
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
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no process groups; background process detection is Unix-only")
	}
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

	// THE SURVIVOR IS DEAD AFTERWARDS. The machinery does not only notice the process that
	// outlived its parent; it ends it, and the test asks the operating system rather than
	// the tool's own line.
	raw, err := os.ReadFile(filepath.Join(b.jobDir(id), "background.pid"))
	if err != nil {
		t.Fatalf("the backgrounded child never recorded its pid: %v", err)
	}
	survivor, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if processIsAlive(survivor) {
		t.Errorf("the process that outlived its parent is still alive (pid %d) after the run", survivor)
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
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
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
	Reaped    int    `json:"reaped,omitempty"`
	Requeued  int    `json:"requeued,omitempty"`
	From      string `json:"from,omitempty"`
}

// D2 (the real run, 2026-09-11): two jobs ended `RUN DONE … dest=failed` and `RUN OK`
// said `started=2 done=2 failed=0`, with both of them in failed/. The counts on RUN OK are
// the truth about the POOL (SPEC-SWARM.md, the output grammar), so they are counted by
// where the job LANDED and not by how the harness exited: a worker that exits 0 and
// publishes no report is a failed task.
func TestAFailedDestinationIsCountedFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker that exits 0 and publishes nothing\nFAKE-NORESULT\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("a failed task is not a failed run; exit %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "rc=0")
	mustContain(t, "the run", stdout, "result=no-result")
	mustContain(t, "the run", stdout, "dest=failed")
	mustContain(t, "the run", stdout, "RUN OK started=1 done=0 failed=1 killed=0 pending=0")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".json")); err != nil {
		t.Errorf("the job counted failed belongs in failed/: %v", err)
	}
}

// D3 (the real run, 2026-09-11): two counts of "findings" on one line. The head's
// `findings: <n>` classifies the report (rule 8), and the `- ` bullets under `## Findings`
// were counted for the RUN line -- so a worker obeying rule 8 with `findings: 0` that
// writes `- none` ended `result=clean findings=1`. One number, and it is the head's.
func TestOneReportHasOneFindingCount(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	b.add("a complete review that found nothing and said so\nFAKE-NONE-BULLET\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "result=clean findings=0")
	if strings.Contains(stdout, "findings=1") {
		t.Errorf("`- none` under ## Findings is not a finding; the head said 0:\n%s", stdout)
	}
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage", stdout, "reports=1 findings=0")
	mustContain(t, "triage", stdout, "clean=1")
}

// Finding 4 (cold read, 2026-09-11): TRIAGE BATCH's new/dup arithmetic double-counted and
// could go NEGATIVE. SPEC-SWARM.md:621 -- "`new` is findings not marked `dup:`". Every
// finding is counted exactly once: marked, owed, folded, or new.
func TestEveryFindingIsCountedOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	b.add("one worker\nFAKE-FINDINGS 1\nFAKE-DUP\n")
	b.add("another worker on the same rev finding the same thing\nFAKE-FINDINGS 1\nFAKE-DUP\n")

	if exit, stdout, stderr := b.run("--workers", "2"); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	exit, stdout, stderr := b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	// Four finding lines: two marked `dup:`, and one (file, line, rule) reported twice
	// under one repo and rev, which folds. One of the four is new.
	mustContain(t, "triage", stdout, "findings=4 new=1 dup=3")
	if strings.Contains(stdout, "new=-") {
		t.Errorf("a count of findings is never negative:\n%s", stdout)
	}
}

// Finding 5 (cold read, 2026-09-11): rule 1's owed-list match was DEAD CODE -- OwedMatch
// was called with a field nothing ever assigned. SPEC-SWARM.md:80: triage "counts a finding
// that matches an owed item and is not marked `dup:` as `duplicate`". `--owed <file>` is
// how the owed list reaches it.
func TestAFindingThatMatchesAnOwedItemIsADuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	b.add("a worker that finds what the pull request already owes\nFAKE-FINDINGS 1\n")
	if exit, stdout, stderr := b.run(); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	owed := filepath.Join(b.dir, "owed.md")
	write(b.t, owed, "- finding 1: the count line prints on failure too\n- something else nobody found\n")

	exit, stdout, stderr := b.swarm("triage", "--pool", b.pool, "--owed", owed, "--all")
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage with an owed list", stdout, "findings=1 new=0 dup=1")

	// Without the owed list the same finding is new: the flag is what makes the difference.
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool, "--all")
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage with no owed list", stdout, "findings=1 new=1 dup=0")
}

// DEMANDED TEST 12, the part this PR could lose without a test noticing (SPEC-SWARM.md:190,
// "and **only then**"): finalize writes the usage file and the report copy BEFORE the job
// moves, and `reclaim` refuses a copy that does not hash to its REV. The cold read's three
// mutations -- claiming the job before settle, deleting the REV comparison, deleting the
// launch transaction's compare-and-swap -- all stayed green; the first two go red here.
func TestUsageAndTheCopyAreWrittenBeforeTheMove(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a job whose evidence outlives it\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	if exit, stdout, stderr := b.run(); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}

	usage := filepath.Join(b.pool, "usage", id+".tsv")
	copyPath := filepath.Join(b.pool, "reports", id, "RESULT.md")
	// THE MOVE's own time is the done/ DIRECTORY's mtime: a rename carries the sidecar's
	// mtime with it, so the file that arrived says when it was written and not when it
	// landed. The directory says when it landed.
	moved := filepath.Join(b.pool, "done")
	usageStat, err := os.Stat(usage)
	if err != nil {
		t.Fatalf("no usage file: %v", err)
	}
	copyStat, err := os.Stat(copyPath)
	if err != nil {
		t.Fatalf("no report copy: %v", err)
	}
	movedStat, err := os.Stat(moved)
	if err != nil {
		t.Fatalf("done/ could not be read: %v", err)
	}
	// "Usage is written ... BEFORE anything else happens to the job" -- the file is older
	// than the move, which is what makes a crash between them survivable.
	if usageStat.ModTime().After(movedStat.ModTime()) {
		t.Errorf("the usage file (%s) is newer than the move (%s): the job moved before its evidence existed",
			usageStat.ModTime(), movedStat.ModTime())
	}
	if copyStat.ModTime().After(movedStat.ModTime()) {
		t.Errorf("the report copy (%s) is newer than the move (%s)", copyStat.ModTime(), movedStat.ModTime())
	}
	// A REV naming the attempt and the copy's SHA-256 sits beside the copy.
	rev, err := os.ReadFile(filepath.Join(b.pool, "reports", id, "REV"))
	if err != nil {
		t.Fatalf("no REV beside the copy: %v", err)
	}
	if !strings.Contains(string(rev), "attempt") {
		t.Errorf("the REV wants the attempt it belongs to:\n%s", rev)
	}

	// Altered bytes after finalize: reclaim refuses and the directory is intact.
	if err := os.WriteFile(copyPath, []byte("a copy nobody wrote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(b.dir, "worker-home-1", "jobs", id)
	exit, stdout, stderr := b.swarm("reclaim", "--pool", b.pool, "--task", id)
	if exit != 1 {
		t.Errorf("a copy that does not match its REV is RECLAIM REFUSED exit 1, got %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "does not match REV")
	if _, err := os.Stat(jobDir); err != nil {
		t.Errorf("a refused reclaim leaves the directory intact: %v", err)
	}
}

// D4 / check 6, and the coordinator's ruling of 2026-09-11 that answered it: the enum said
// `opencode` and the reader read a tab-separated file no OpenCode writes, so two jobs that
// burned 61,875 and 85,308 tokens both reported `budget=-/20000`. A source is named for what
// it IS -- and what it is, is now true: `usage: opencode` reads the job's own
// `opencode.db` through `sqlite3`, read-only (SPEC-SWARM rule 12, rule 13). `tsv` is not a
// name a worker description may carry: the file is what the FAKE harness writes, never a
// source a caller may name.
func TestTheUsageSourceIsTheDatabaseTheHarnessWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a job whose numbers come from the database\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - 20 -\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("`usage: opencode` runs; exit %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE id="+id)
	row := b.usageRow(id)
	for _, c := range []struct{ column, want string }{
		{"tokens_in", "100"}, {"tokens_out", "50"}, {"cache_write", "-"}, {"cache_read", "20"}, {"reasoning", "-"},
	} {
		if row[c.column] != c.want {
			t.Errorf("the row read from opencode.db wants %s=%s, got %q", c.column, c.want, row[c.column])
		}
	}

	// The retired name is refused BEFORE the first worker, the way any name that is not a
	// source is: a caller who names the tab-separated file is told the two sources there are.
	b.rewriteWorker(func(d map[string]any) { d["usage"] = "tsv" })
	b.add("a task nothing will run\n")
	exit, stdout, stderr = b.run()
	if exit != 2 {
		t.Fatalf("`usage: tsv` exits %d, want 2 -- it is not a source a description may name:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "opencode")
	mustContain(t, "the refusal", stderr, "none")
	if strings.Contains(stdout, "RUN START") {
		t.Errorf("the refusal comes before any worker starts:\n%s", stdout)
	}
}

// DEMANDED TEST 9 (SPEC-SWARM.md:1264). N workers are N CHILD PROCESSES, each with its own
// job directory and its own report file; a tripwire on every path a child opens for
// WRITING finds no path opened by two children; the merge into the page runs once, after
// the last worker is reaped.
//
// This is the prototype's worst failure written as an assertion: workers that shared a
// working directory shared a harness database, and two jobs' token counts, two jobs' logs
// and two jobs' reports landed on top of each other. Slots exist for this, and the fake
// harness records every path it opens for writing so the test can prove it rather than
// trust it.
func TestNWorkersAreNProcessesAndShareNoPath(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	var ids []string
	for i := 0; i < 4; i++ {
		ids = append(ids, b.add("one of four workers, two at a time\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n"))
	}

	// Four jobs over two workers: two run at once, and each slot runs two jobs one after
	// the other. Both halves matter -- a path shared by two workers and a path shared by
	// two jobs of ONE worker are the same lost token count.
	exit, stdout, stderr := b.run("--workers", "2")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}

	// N child processes: N distinct supervisor pids, N distinct slots, N distinct job
	// directories, N report files.
	pids := map[int]string{}
	slots := map[int]string{}
	dirs := map[string]bool{}
	for _, id := range ids {
		var pr struct {
			Pid  int    `json:"pid"`
			Slot int    `json:"slot"`
			Job  string `json:"job"`
		}
		dir := b.jobDir(id)
		dirs[dir] = true
		raw, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil {
			t.Fatalf("no pid record for %s: %v", id, err)
		}
		if err := json.Unmarshal(raw, &pr); err != nil {
			t.Fatal(err)
		}
		if other, seen := pids[pr.Pid]; seen {
			t.Errorf("%s and %s were the same process (pid %d): N workers are N processes", other, id, pr.Pid)
		}
		pids[pr.Pid] = id
		if pr.Slot < 1 || pr.Slot > 2 {
			t.Errorf("%s ran on slot %d, outside the two workers this run allows", id, pr.Slot)
		}
		slots[pr.Slot] = id
		if _, err := os.Stat(filepath.Join(b.pool, "reports", id, "RESULT.md")); err != nil {
			t.Errorf("%s has no report file of its own: %v", id, err)
		}
	}
	if len(dirs) != len(ids) {
		t.Errorf("%d jobs want %d job directories, got %d", len(ids), len(ids), len(dirs))
	}

	// THE TRIPWIRE. Every path a child opened for writing, by job: no path twice.
	owner := map[string]string{}
	for _, id := range ids {
		raw, err := os.ReadFile(filepath.Join(b.jobDir(id), "writes"))
		if err != nil {
			t.Fatalf("the child for %s recorded no write path: %v", id, err)
		}
		paths := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(paths) < 3 {
			t.Fatalf("the tripwire wants every write path of %s, got %d", id, len(paths))
		}
		for _, path := range paths {
			if other, seen := owner[path]; seen && other != id {
				t.Errorf("two children opened %s for writing: %s and %s", path, other, id)
			}
			owner[path] = id
		}
	}

	// The merge into the page runs ONCE, after the last worker is reaped: one page, holding
	// all three, written by the triage that follows the run and never by a worker.
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	pages := 0
	entries, err := os.ReadDir(filepath.Join(b.pool, "reports"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			pages++
		}
	}
	if pages != 1 {
		t.Errorf("one merge is one page, got %d", pages)
	}
	page, err := os.ReadFile(field(t, stdout, "page="))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !strings.Contains(string(page), id) {
			t.Errorf("the one page wants every reaped worker's report, %s is missing", id)
		}
	}
}

// processIsAlive asks the operating system about ONE pid this test was handed. It never
// scans a process table and never matches a command line.
func processIsAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// lineWith is the one line of an output that carries a marker.
func lineWith(t *testing.T, out, marker string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("no line carrying %q in:\n%s", marker, out)
	return ""
}

// jobDir is one job's directory, wherever its slot put it.
func (b *bench) jobDir(id string) string {
	b.t.Helper()
	for slot := 1; slot <= swarmSlotsInTests; slot++ {
		dir := filepath.Join(b.dir, fmt.Sprintf("worker-home-%d", slot), "jobs", id)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	b.t.Fatalf("no job directory for %s under %s", id, b.dir)
	return ""
}

// swarmSlotsInTests is the highest slot any test here runs with.
const swarmSlotsInTests = 8

// DEMANDED TEST 12 (SPEC-SWARM.md:1279). USAGE OUTLIVES THE JOB, and every sentence of
// `RECLAIM REFUSED` is one of these assertions.
//
// DeepSeek's usage for two batches on 2026-09-11 lived in per-worker data directories that
// were reclaimed with the jobs, and nothing survived. So the usage file is written OUTSIDE
// the reclaimable subtree, BEFORE anything moves, and reclaim -- the one thing this tool
// deletes -- refuses without both the usage file and the verified report copy.
func TestUsageOutlivesTheJob(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a job whose evidence outlives it\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	if exit, stdout, stderr := b.run(); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	usage := filepath.Join(b.pool, "usage", id+".tsv")
	row := b.usageRow(id)
	if row["end"] != "done" || row["attempt"] != "1" {
		t.Errorf("a job that finished wants end=done attempt=1, got end=%q attempt=%q", row["end"], row["attempt"])
	}
	// `repo` is a dash: the message table the source reads holds no repository, and a
	// reader that invented one would be writing a fact nobody measured (rule 12, a field
	// the provider did not report is the literal dash).
	for col, want := range map[string]string{"tokens_in": "100", "tokens_out": "50", "cache_write": "-",
		"cache_read": "-", "reasoning": "-", "model": "fake-model", "repo": "-"} {
		if row[col] != want {
			t.Errorf("the usage row wants %s=%s, got %q", col, want, row[col])
		}
	}
	// The file is older than the MOVE. A rename carries the sidecar's own mtime with it, so
	// the arrived file says when it was written and never when it landed; the done/
	// DIRECTORY's mtime is the move's own time.
	usageStat, err := os.Stat(usage)
	if err != nil {
		t.Fatalf("no usage file: %v", err)
	}
	movedStat, err := os.Stat(filepath.Join(b.pool, "done"))
	if err != nil {
		t.Fatal(err)
	}
	if usageStat.ModTime().After(movedStat.ModTime()) {
		t.Errorf("the usage file (%s) is newer than the move (%s): the job moved before its evidence existed",
			usageStat.ModTime(), movedStat.ModTime())
	}

	jobDir := b.jobDir(id)
	copyPath := filepath.Join(b.pool, "reports", id, "RESULT.md")
	kept := filepath.Join(b.dir, "kept-usage.tsv")

	// (a) NO USAGE FILE: refused, exit 1, the directory intact.
	raw, err := os.ReadFile(usage)
	if err != nil {
		t.Fatal(err)
	}
	write(t, kept, string(raw))
	if err := os.Remove(usage); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := b.swarm("reclaim", "--pool", b.pool, "--task", id)
	if exit != 1 {
		t.Errorf("a reclaim with no usage file exits %d, want 1:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "no usage file")
	if _, err := os.Stat(jobDir); err != nil {
		t.Errorf("a refused reclaim leaves the directory intact: %v", err)
	}
	write(t, usage, string(raw))

	// (b) NO REPORT COPY: refused, exit 1, the directory intact -- and `finalize` is the
	// verb that writes the copy, which is why the refusal names it.
	if err := os.Remove(copyPath); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr = b.swarm("reclaim", "--pool", b.pool, "--task", id)
	if exit != 1 {
		t.Errorf("a reclaim with no report copy exits %d, want 1:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "no report copy")
	mustContain(t, "the refusal", stderr, "nova-swarm finalize")
	if _, err := os.Stat(jobDir); err != nil {
		t.Errorf("a refused reclaim leaves the directory intact: %v", err)
	}
	if exit, stdout, stderr = b.swarm("finalize", "--pool", b.pool, "--task", id); exit != 0 {
		t.Fatalf("finalize exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "finalize", stdout, "FINALIZE OK id="+id)
	if _, err := os.Stat(copyPath); err != nil {
		t.Errorf("finalize writes the copy a reclaim was refused for: %v", err)
	}

	// (c) With both, the directory goes and `cost` still answers, which is the whole point.
	exit, stdout, stderr = b.swarm("reclaim", "--pool", b.pool, "--task", id)
	if exit != 0 {
		t.Fatalf("reclaim exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "reclaim", stdout, "RECLAIM OK id="+id)
	if _, err := os.Stat(jobDir); err == nil {
		t.Error("a reclaim that printed OK removed nothing")
	}
	exit, stdout, _ = b.swarm("cost", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("cost exited %d", exit)
	}
	mustContain(t, "cost", stdout, "COST OK tasks=1 in=100 out=50")
	// A provider that reported no cache counts prints dashes, never zeroes.
	mustContain(t, "cost", stdout, "cache_write=- cache_read=-")
	mustContain(t, "cost", stdout, "dashes=0,0,1,1,1")
}

// Demanded test 12, the two records that stand in for a report: a MALFORMED job reclaims
// only with the MALFORMED marker present, and a no-result job only with NO-RESULT. The
// marker is the record of WHY there is no foldable report, and a reclaim that removed the
// job directory without it would leave a pool that cannot say what happened.
func TestAMarkerIsTheRecordWhereThereIsNoReport(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	malformed := b.add("a report with a fourth state word\nFAKE-FINDINGS 1\nFAKE-MALFORMED\n")
	noResult := b.add("a worker that publishes nothing\nFAKE-NORESULT\n")
	if exit, stdout, stderr := b.run("--workers", "2"); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}

	marker := filepath.Join(b.pool, "reports", malformed, "MALFORMED")
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("a malformed report wants its MALFORMED marker: %v", err)
	}
	mustContain(t, "the marker", string(body), "line=")
	if _, err := os.Stat(filepath.Join(b.pool, "reports", noResult, "NO-RESULT")); err != nil {
		t.Errorf("a job that published nothing wants its NO-RESULT marker: %v", err)
	}
	for _, id := range []string{malformed, noResult} {
		if _, err := os.Stat(filepath.Join(b.pool, "reports", id, "REV")); err != nil {
			t.Errorf("a REV sits beside every copy AND every marker, %s has none: %v", id, err)
		}
	}

	// Without its marker, neither reclaims.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := b.swarm("reclaim", "--pool", b.pool, "--task", malformed)
	if exit != 1 {
		t.Errorf("a malformed job with no MALFORMED marker exits %d, want 1:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "MALFORMED")
	if _, err := os.Stat(b.jobDir(malformed)); err != nil {
		t.Errorf("a refused reclaim leaves the directory intact: %v", err)
	}
	if err := os.Remove(filepath.Join(b.pool, "reports", noResult, "NO-RESULT")); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr = b.swarm("reclaim", "--pool", b.pool, "--task", noResult)
	if exit != 1 {
		t.Errorf("a no-result job with no NO-RESULT marker exits %d, want 1:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "no report copy")
	if _, err := os.Stat(b.jobDir(noResult)); err != nil {
		t.Errorf("a refused reclaim leaves the directory intact: %v", err)
	}
}

// usageRow reads one job's usage file as a map of column to value.
func (b *bench) usageRow(id string) map[string]string {
	b.t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.pool, "usage", id+".tsv"))
	if err != nil {
		b.t.Fatalf("no usage file for %s: %v", id, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		b.t.Fatalf("a usage file is one header and one row, got %d lines:\n%s", len(lines), raw)
	}
	head, values := strings.Split(lines[0], "\t"), strings.Split(lines[1], "\t")
	row := map[string]string{}
	for i, name := range head {
		if i < len(values) {
			row[name] = values[i]
		}
	}
	return row
}

// DEMANDED TEST 13 (SPEC-SWARM.md:1307). A BUDGET ENDS THE JOB AND KEEPS THE FINDINGS.
//
// The budget is a STOP CONDITION ON OBSERVATIONS, not a ceiling on spend: usage arrives
// after the tokens are gone, so the overshoot is bounded by one sample and the row carries
// the true final sum. What must never happen is the thing the prototype did -- end the job
// and lose what it had already found.
func TestBudgetEndsTheJobAndKeepsFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	over := b.add("a worker that publishes a finding and then spends past its budget\nFAKE-PUBLISH-FIRST\nFAKE-FINDINGS 1\nFAKE-USAGE 30000 0 - - -\nFAKE-SLEEP 30\n",
		"--tokens", "1000")
	under := b.add("a worker that stays well under its budget\nFAKE-PUBLISH-FIRST\nFAKE-FINDINGS 1\nFAKE-USAGE 10 5 - - -\n",
		"--tokens", "1000")

	exit, stdout, stderr := b.run("--workers", "2", "--usage-interval", "1")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN BUDGET id="+over)
	mustContain(t, "the run", stdout, "of=1000 findings=1")
	// The `spent=` on the line is AT LEAST the budget: the sample that stopped the job is
	// the one that saw the budget passed.
	spent := field(t, stdout, "spent=")
	if n, err := strconv.Atoi(spent); err != nil || n < 1000 {
		t.Errorf("RUN BUDGET wants spent= at least the budget, got %q", spent)
	}
	// THE FINDING STANDS. The report the worker had published is copied, whole, findings
	// and all -- the budget ended the job, it did not throw the work away.
	copied, err := os.ReadFile(filepath.Join(b.pool, "reports", over, "RESULT.md"))
	if err != nil {
		t.Fatalf("a budgeted job keeps its published report: %v", err)
	}
	mustContain(t, "the kept report", string(copied), "- finding 1:")
	row := b.usageRow(over)
	if row["end"] != "budget" {
		t.Errorf("the usage row of a budgeted job wants end=budget, got %q", row["end"])
	}
	if row["tokens_in"] != "30000" {
		t.Errorf("the row carries the FINAL sum the source held, not the sum at the stop: got in=%q", row["tokens_in"])
	}

	// A job under its budget is untouched.
	mustContain(t, "the run", stdout, "RUN DONE id="+under)
	if strings.Contains(stdout, "RUN BUDGET id="+under) {
		t.Errorf("a job under its budget is not ended by it:\n%s", stdout)
	}
	if got := b.usageRow(under)["end"]; got != "done" {
		t.Errorf("the job under budget wants end=done, got %q", got)
	}
}

// Demanded test 13: `--tokens unmetered` prints `budget=unmetered` and runs to its
// deadline. A caller who says the budget is not this tool's business says it ONCE, in a
// word, and a number is never guessed from it.
func TestAnUnmeteredJobRunsToItsDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker with no token budget at all\nFAKE-FINDINGS 1\nFAKE-USAGE 999999 999999 - - -\n",
		"--tokens", "unmetered")
	exit, stdout, stderr := b.run("--usage-interval", "1")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE id="+id)
	mustContain(t, "the run", stdout, "budget=unmetered")
	if strings.Contains(stdout, "RUN BUDGET") {
		t.Errorf("an unmetered job has no budget to end it:\n%s", stdout)
	}
	if got := b.usageRow(id)["end"]; got != "done" {
		t.Errorf("an unmetered job that finished wants end=done, got %q", got)
	}
}

// Demanded test 13: a worker description that says `usage: none` beside a PENDING NUMERIC
// budget is exit 2 BEFORE any worker starts, naming the task -- a budget nothing can
// observe is a promise this tool cannot keep -- and beside `unmetered` tasks it runs.
func TestANumericBudgetWithNoUsageSourceIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	metered := b.add("a task carrying a number nothing can watch\nFAKE-FINDINGS 1\n", "--tokens", "5000")
	b.rewriteWorker(func(d map[string]any) { d["usage"] = "none" })

	exit, stdout, stderr := b.run()
	if exit != 2 {
		t.Fatalf("`usage: none` beside a numeric budget exits %d, want 2:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, metered)
	mustContain(t, "the refusal", stderr, "usage: none")
	mustContain(t, "the refusal", stderr, "--tokens unmetered")
	if strings.Contains(stdout, "RUN START") {
		t.Errorf("the refusal comes before any worker starts:\n%s", stdout)
	}

	// The same description beside an unmetered task RUNS: the refusal is about the promise,
	// not about the source.
	for _, suffix := range []string{".task", ".json"} {
		if err := os.Remove(filepath.Join(b.pool, "pending", metered+suffix)); err != nil {
			t.Fatal(err)
		}
	}
	unmetered := b.add("a task that asks for no budget\nFAKE-FINDINGS 1\n", "--tokens", "unmetered")
	exit, stdout, stderr = b.run()
	if exit != 0 {
		t.Fatalf("`usage: none` beside unmetered tasks runs; exit %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE id="+unmetered)
	mustContain(t, "the run", stdout, "budget=unmetered")
}

// Demanded test 13, the half that matters most: a usage source that ERRORS is not a source
// that reports nothing. Three consecutive failures end the job `RUN BUDGET-UNVERIFIABLE
// samples=3` with the findings kept, because a numeric budget the tool has stopped being
// able to see is a budget the caller believes is enforced and is not.
func TestAnUnreadableUsageSourceEndsTheJobUnverifiable(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a file mode that refuses its owner is a unix fact")
	}
	b := newBench(t)
	id := b.add("a worker whose usage source cannot be read\nFAKE-PUBLISH-FIRST\nFAKE-FINDINGS 2\nFAKE-BADUSAGE\nFAKE-SLEEP 30\n",
		"--tokens", "5000")
	exit, stdout, stderr := b.run("--usage-interval", "250ms")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN BUDGET-UNVERIFIABLE id="+id)
	mustContain(t, "the run", stdout, "samples=3 findings=2")
	if got := b.usageRow(id)["end"]; got != "budget-unverifiable" {
		t.Errorf("the row of an unverifiable job wants end=budget-unverifiable, got %q", got)
	}
	copied, err := os.ReadFile(filepath.Join(b.pool, "reports", id, "RESULT.md"))
	if err != nil {
		t.Fatalf("the findings are KEPT when the budget cannot be verified: %v", err)
	}
	mustContain(t, "the kept report", string(copied), "- finding 2:")
}

// AUDIT F1 and F4 (the new-user audit, 2026-09-11): a harness that exits 7 on a bad key
// printed `RUN DONE … rc=7 … dest=failed` and then `RUN OK started=1 done=1 failed=0`,
// exit 0, and `COST TASK … end=done` for a job in failed/. The counting was fixed by D2;
// `End` was not. SPEC-SWARM.md:544: "A worker that exits non-zero moves its files to
// `failed/` and the pass continues; `RUN OK` carries `failed=<n>`." The `end` column is
// what the token ledger reads, so a spent failure read as a spent success is a wrong
// number in a report to Glenn.
//
// The fake harness has understood FAKE-RC since the day it was written and no test had ever
// used it: the machinery to catch this was built and never fired.
func TestAWorkerThatExitsNonZeroIsAFailedJobEverywhere(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker whose provider refuses its key\nFAKE-FINDINGS 1\nFAKE-USAGE 40 20 - - -\nFAKE-RC 7\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("a failed task is not a failed run; exit %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "rc=7")
	mustContain(t, "the run", stdout, "dest=failed")
	// THE COUNT IS THE POOL'S TRUTH.
	mustContain(t, "the run", stdout, "RUN OK started=1 done=0 failed=1 killed=0 pending=0")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".json")); err != nil {
		t.Errorf("a job whose worker exited 7 belongs in failed/: %v", err)
	}
	// AND THE LEDGER SAYS SO. `end` is the column nova-tokens reads.
	if got := b.usageRow(id)["end"]; got != "failed" {
		t.Errorf("the usage row of a job whose worker exited 7 wants end=failed, got %q", got)
	}
	if got := b.usageRow(id)["rc"]; got != "7" {
		t.Errorf("the usage row wants rc=7, got %q", got)
	}
	exit, stdout, stderr = b.swarm("cost", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("cost exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "cost", stdout, "end=failed")
	if strings.Contains(stdout, "end=done") {
		t.Errorf("the cost ledger called a failed job done:\n%s", stdout)
	}
}

// TestSupervisorLogAppendsNeverTruncates is TestBatchLogAppendsNeverTruncates
// (internal/swarm) for the OTHER writer of a card's `<job>/harness.log`: the supervisor,
// which pins the harness's own stdout and stderr to that file. Two processes write it for
// one job and each holds its own offset -- a `batch` pins its runner's stdout to the same
// path before this supervisor starts -- so a truncating open here starts at offset 0 and
// writes over the head of what the runner already put there. That is how the start of a
// card's evidence was lost (issue #608), and it is the same defect on the same file from
// the other side, so it gets the same test: write a line, run the job, demand BOTH sets of
// bytes in the order they were written. Red with O_TRUNC in supervise.go.
func TestSupervisorLogAppendsNeverTruncates(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	const said = "the-harness-said-this"
	id := b.add("a task whose harness says one line\nFAKE-SAY " + said + "\nFAKE-FINDINGS 1\n")

	// The bytes the other writer put there before the supervisor opened the file. The job
	// directory is the slot's, named before the run the way the dispatcher will name it.
	job := filepath.Join(b.dir, "worker-home-1", "jobs", id)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	const earlier = "the runner wrote this before the supervisor started"
	write(t, filepath.Join(job, "harness.log"), earlier+"\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s%s", exit, stdout, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(job, "harness.log"))
	if err != nil {
		t.Fatalf("the job's harness log could not be read: %v", err)
	}
	got := string(raw)
	at, after := strings.Index(got, earlier), strings.Index(got, said)
	if at < 0 {
		t.Errorf("the supervisor truncated the job's harness log: the bytes written before the run are gone:\n%s", got)
	}
	if after < 0 {
		t.Fatalf("the harness's own line is not in the job's harness log:\n%s", got)
	}
	if at >= 0 && after < at {
		t.Errorf("the harness's line landed before the bytes that were there first; the log is out of order:\n%s", got)
	}
}

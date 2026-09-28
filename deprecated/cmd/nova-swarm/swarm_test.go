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
	t.Cleanup(func() { reapLeftoverSupervise(b) })
	if err := os.MkdirAll(b.pool, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(b.pool, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
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
	// THE PROVIDER RETRY WAIT IS NOT UNDER TEST HERE. A launch that dies on a provider 5xx
	// is retried after 5-20s and then 30-60s (swarm.ProviderRetryDelay); a verdict test
	// that drives FAKE-5XX through all three launches sat up to 80s in those waits and
	// asserted nothing about them. The bands are internal/swarm's TestProviderRetryDelayBands;
	// here every run pins the wait to zero through the seam the delay already reads, once,
	// for the whole process, so no test needs t.Setenv (which t.Parallel refuses) for it. A
	// test that wants a wait of its own still sets it.
	if os.Getenv("NOVA_SWARM_PROVIDER_BACKOFF") == "" {
		_ = os.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "0s")
	}
	if err := buildShared(); err != nil {
		fmt.Fprintf(os.Stderr, "building the binaries these tests run: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if leftover := leftoverChildPIDs(); len(leftover) > 0 {
		for _, pid := range leftover {
			fmt.Fprintf(os.Stderr, "a child of the test binary (pid %d) was still alive at exit\n", pid)
			reapLeftoverPID(pid)
		}
		if code == 0 {
			code = 1
		}
	}
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

// triage accepts --usage and exits 0.
func TestTriageAcceptsUsageFlag(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	usageFile := filepath.Join(t.TempDir(), "usage.tsv")
	exit, stdout, stderr := b.swarm("triage", "--pool", b.pool, "--usage", usageFile)
	if exit != 0 {
		t.Fatalf("triage with --usage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage", stdout, "TRIAGE BATCH ")
}

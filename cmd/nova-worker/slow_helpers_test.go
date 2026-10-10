//go:build slow || functional

// The helpers of the slow and functional tiers: every caller sits in a file behind the
// `slow` or `functional` tag (slow_test.go and the *_functional_test.go files), so these
// sit behind the same tags. Left in an untagged file they are dead code to the unit
// tier, and staticcheck, which reads the tree untagged, reports each as U1000.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreEnv(key, val string) {
	if val != "" {
		os.Setenv(key, val)
	} else {
		os.Unsetenv(key)
	}
}

// pushBench is one member root with an origin (a bare repository with main
// at one commit), a work card c1 whose brief names that origin, and the
// card's checkout where native stages it: <slots>/<launch>/jobs/c1/repo, a
// clone of origin on the branch staging names.
type pushBench struct {
	root, slots, origin, checkout, base string
	p                                   member.Packet
}

func newPushBench(t *testing.T) *pushBench {
	t.Helper()
	root := t.TempDir()
	b := &pushBench{root: root, slots: filepath.Join(root, "slots"), origin: filepath.Join(root, "origin.git")}
	seed := filepath.Join(root, "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	require.NoError(t, os.WriteFile(filepath.Join(seed, "f"), []byte("base\n"), 0o644))
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, b.origin)
	b.base = gitAs(t, b.origin, "rev-parse", "main")
	b.p = member.Packet{Card: "c1", Kind: "work", As: "m1", Primary: "p1", Stream: "s1", Attempt: 1, Gen: 1, Epoch: 7,
		Brief: "RESULT: c1\nbase-repo: " + b.origin + "\nBASE: main\n\nDo the work.", Branch: "sprint/c1"}
	b.checkout = filepath.Join(b.slots, launchName(b.p), "jobs", "c1", swarm.JobRepo)
	require.NoError(t, os.MkdirAll(filepath.Dir(b.checkout), 0o755))
	runGit(t, "", "clone", "-q", "--", b.origin, b.checkout)
	gitAs(t, b.checkout, "switch", "-q", "-c", "rowan/c1")
	b.staged(t, b.base)
	return b
}

// staged records the commit native staged the launch at, in the slot, as
// native does (cardcontract.StagedName).
func (b *pushBench) staged(t *testing.T, sha string) {
	t.Helper()
	write(t, filepath.Join(b.slots, launchName(b.p), "staged"), sha+"\n")
}

// commit is a commit of the child's in the checkout; its sha.
func (b *pushBench) commit(t *testing.T, text string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(b.checkout, "f"), []byte(text), 0o644))
	gitAs(t, b.checkout, "commit", "-q", "-am", text)
	return gitAs(t, b.checkout, "rev-parse", "HEAD")
}

// secondLine is a second line of work in the checkout: a branch from the staged commit with a
// commit of its own; the checkout is left on rowan/c1. Its sha.
func (b *pushBench) secondLine(t *testing.T) string {
	t.Helper()
	gitAs(t, b.checkout, "switch", "-q", "-c", "other", b.base)
	other := b.commit(t, "another line\n")
	gitAs(t, b.checkout, "switch", "-q", "rowan/c1")
	return other
}

// originHas is the sha origin's branch holds, "" when it has none.
func (b *pushBench) originHas(t *testing.T, branch string) string {
	t.Helper()
	out := strings.TrimSpace(runGit(t, b.origin, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch))
	return out
}

func (b *pushBench) pusher() *gitPusher { return newGitPusher(b.root, b.slots) }

// recordGitRun is a git that records every argv and directory it is asked for
// and runs it, or refuses a push with the line given.
type recordGitRun struct {
	mu         sync.Mutex
	calls      []gitrun.Options
	argv       [][]string
	refusePush string
}

func (r *recordGitRun) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	r.mu.Lock()
	r.calls, r.argv = append(r.calls, o), append(r.argv, slices.Clone(args))
	r.mu.Unlock()
	if r.refusePush != "" && slices.Contains(args, "push") {
		return gitrun.Result{Stderr: []byte("To the origin\n" + r.refusePush + "\n")}, assert.AnError
	}
	return gitrun.Run(ctx, o, args...)
}

// rejectGit is a git whose first n pushes origin rejects on its own side, git's
// `[remote rejected]` line; every other git, and the pushes after, run.
type rejectGit struct {
	mu     sync.Mutex
	reject int
	pushes int
}

func (r *rejectGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	if slices.Contains(args, "push") {
		r.mu.Lock()
		r.pushes++
		rejected := r.pushes <= r.reject
		r.mu.Unlock()
		if rejected {
			return gitrun.Result{Stdout: []byte("To the origin\n!\t0123:refs/heads/sprint/c1\t[remote rejected] (failed)\nDone\n")}, assert.AnError
		}
	}
	return gitrun.Run(ctx, o, args...)
}

// badFetchGit is a git whose first n fetches into the push repository fail as one did on the
// 5000-card load test (2026-10-01), on another launch's ref, while the bench mirror the push
// repository borrows objects from was being repacked; every other git, and the fetches
// after, run. Failed tries first run the real fetch, then inject an error after refs
// exist. It records those refs and the refs held before each fetch and push.
type badFetchGit struct {
	mu               sync.Mutex
	fail             int
	fetches          int
	refsAtPush       []string
	refsAtFetch      []string
	refsAfterFailure [][]string
}

const badFetchLine = "fatal: bad object refs/member/c9.w1.g1.e7/HEAD"

func (r *badFetchGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	held := func() []string {
		res, err := gitrun.Run(ctx, gitrun.Options{C: o.C}, "for-each-ref", "--format=%(refname)", "refs/member/")
		if err != nil {
			return []string{"for-each-ref: " + err.Error()}
		}
		return strings.Fields(string(res.Stdout))
	}
	switch {
	case slices.Contains(args, "fetch"):
		r.mu.Lock()
		r.fetches++
		failed := r.fetches <= r.fail
		r.refsAtFetch = append(r.refsAtFetch, held()...)
		r.mu.Unlock()
		res, err := gitrun.Run(ctx, o, args...)
		if err != nil {
			return res, err // a real fetch failure is never replaced by the injected one
		}
		if failed {
			r.mu.Lock()
			r.refsAfterFailure = append(r.refsAfterFailure, held())
			r.mu.Unlock()
			return gitrun.Result{Stderr: []byte(badFetchLine + "\n")}, assert.AnError
		}
		return res, nil
	case slices.Contains(args, "push"):
		r.mu.Lock()
		r.refsAtPush = append(r.refsAtPush, held()...)
		r.mu.Unlock()
	}
	return gitrun.Run(ctx, o, args...)
}

// wrongTail is a sha whose first twelve characters are head's and whose tail is invented, as
// a cheap model wrote one on the 1000-card load test (2026-10-01).
func wrongTail(head string) string {
	tail := "0123456789abcdef0123456789ab"
	if head[12:] == tail {
		tail = "ba9876543210fedcba9876543210"
	}
	return head[:12] + tail
}

// budgetNativeArgs is one complete native argv with the budget word the caller names, and
// with it omitted entirely when the word is empty: the two shapes every case here wants.
func budgetNativeArgs(t *testing.T, bin, card, slot, root, tokens string) []string {
	t.Helper()
	args := []string{"native",
		"--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", card, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	if tokens != "" {
		args = append(args, "--tokens", tokens)
	}
	return args
}

// budgetCard writes a card the fake harness will run through without spending anything.
func budgetCard(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(path, []byte("a card\nFAKE-FINDINGS 1\n"), 0o644))
	return path
}

// nativeOKLine is the one NATIVE verdict line in a capture, failed for if there is none.
//
// THE VERDICT WORD IS NOT THIS HELPER'S SUBJECT (nova-tools #1844, which landed on dev
// while this branch was open). `native` now prints `NATIVE OK` only for a run that earned
// it and `NATIVE INCOMPLETE ... why=<harness-silent|no-result|rc>` otherwise, and #1844's
// own sentence is that "every other field is byte-for-byte the same, so a reader that
// parses fields still reads them all". Every caller here asserts a FIELD -- budget=,
// stopped=, rc=, harness= -- so the helper matches the line by its `NATIVE ` prefix and
// lets the verdict be whatever the run earned. A card the budget stopped has rc=-1 and so
// is INCOMPLETE by #1844's rule, which is the truth about it: it did not finish. The
// verdict's OWN assertions live in native_verdict_test.go and are untouched.
func nativeOKLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "NATIVE OK ") || strings.HasPrefix(line, "NATIVE INCOMPLETE ") {
			return line
		}
	}
	t.Fatalf("no NATIVE verdict line in:\n%s", out)
	return ""
}

// providerRun is one card run through `native` with the fake harness: before, when set,
// is called with the slot's data home before the run begins (what an earlier run left).
func providerRun(t *testing.T, label, card string, before func(data string)) (stdout, stderr string) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	if before != nil {
		before(filepath.Join(slot, "data"))
	}
	cardPath := filepath.Join(root, label+".md")
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
	var out, errb bytes.Buffer
	run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--tokens", "unmetered", "--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &out, &errb, time.Now())
	return out.String(), errb.String()
}

// olderRunsError is what a slot's log holds from a run before this one: it names an
// error the run under test never had, and is never the reason.
func olderRunsError(data string) {
	path := filepath.Join(data, "opencode", "log", "opencode.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("timestamp=2030-01-01T00:00:00.000Z level=ERROR run=00000000 message=\"stream error\" error.error=\"an older run's rate limit\"\n"), 0o644)
}

// nativeSandbox returns the fake sandbox of the seam tests: a stand-in for nova-sandbox that
// records its argv and, under NOVA_FAKE_SANDBOX=hosts, reports hosts=enforceable so the
// repo allow rule reaches the argv. Its compile is shared by every test that asks.
func nativeSandbox(t *testing.T) string {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	return builtFakeSandbox
}

// nativeSandboxOnPath puts the fake sandbox on PATH under its own name (`nova-sandbox`), so
// the native run resolves the wall itself rather than being handed a --sandbox path. It
// returns the directory that now names the wall on PATH; the stand-in itself is built once
// and linked there, never compiled per test.
func nativeSandboxOnPath(t *testing.T) string {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	dir := builtPathBin
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// sandboxArgv reads the argv the wall recorded into the job directory, if any.
func sandboxArgv(t *testing.T, jobDir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(jobDir, "sandbox-argv"))
	require.NoError(t, err, "the wall recorded no argv under %s", jobDir)
	return string(raw)
}

// hasFlagPairResolved is hasFlagPair with the value compared by symlink-resolved path: on
// macOS t.TempDir() lands under /var -> /private/var, so the wall argv carries the resolved
// spelling while the test holds the unresolved one.
func hasFlagPairResolved(argv []string, flag, want string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			if got, err := filepath.EvalSymlinks(argv[i+1]); err == nil && got == want {
				return true
			}
		}
	}
	return false
}

// assertConfigRecord reads what the fake harness recorded about the config at its own XDG
// data home path: the mode it found, and one thing that must be in the bytes it read.
func assertConfigRecord(t *testing.T, slot, wantMode, wantBody string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "lbl", "config-record"))
	require.NoError(t, err, "the harness recorded no config-record")
	rec := string(raw)
	if wantMode == "absent" {
		assert.Equal(t, "absent\n", rec, "the harness saw a config where none should be: %q", rec)
		return
	}
	assert.True(t, strings.HasPrefix(rec, "mode="+wantMode+"\n"), "the harness saw %q, want mode %s", rec, wantMode)
	assert.True(t, wantBody == "" || strings.Contains(rec, wantBody), "the harness saw no %s in the config it read:\n%s", wantBody, rec)
}

func assertRepoRefusal(t *testing.T, out string) {
	t.Helper()
	require.Contains(t, out, "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", out)
	require.Contains(t, out, "lbl wall cannot express repo rule", "the refusal names the label and the reason, got:\n%s", out)
	got := strings.Count(strings.TrimSpace(out), "\n") + 1
	require.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, out)
}

// nativeLoggedEnv reads the env lines of a native-argv.log into a name -> values map.
func nativeLoggedEnv(t *testing.T, log string) map[string][]string {
	t.Helper()
	m := map[string][]string{}
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "env: ") {
			continue
		}
		kv := strings.TrimPrefix(line, "env: ")
		name, val, _ := strings.Cut(kv, "=")
		m[name] = append(m[name], val)
	}
	return m
}

// resolvedPath is a path made absolute and symlink-resolved, which is the form admission
// records. A test that builds its expectation from t.TempDir() must resolve it too: on
// darwin the temp directory is handed out under /var, a symlink to /private/var, so the
// unresolved spelling and the recorded one are two names for one directory and a string
// compare between them fails on every macOS bench (issue #578).
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	require.NoError(t, err, "resolving %s", path)
	return real
}

// nativeWorkerDescription writes a worker description the native run can be pointed at: the
// model it pins, and the key named either by the legacy key_file (for --auth) or by the
// `secret` variable a nova-secrets exec would deliver.
func nativeWorkerDescription(t *testing.T, model, keyShape string) string {
	t.Helper()
	home := t.TempDir()
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": model,
		"env_var": "FAKE_KEY", "usage": "opencode",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
	}
	if keyShape == "secret" {
		desc["secret"] = "FAKE_KEY"
	} else {
		key := filepath.Join(t.TempDir(), "key")
		require.NoError(t, os.WriteFile(key, []byte("FAKE_KEY="+fakeKey+"\n"), 0o600))
		desc["key_file"] = key
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "worker.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))
	return path
}

// needsSQLite skips a case on a bench with no reader, naming it. On such a bench a numeric
// budget is a NATIVE REFUSED (rule 13d, slice 2), which is the rule working.
func needsSQLite(t *testing.T) {
	t.Helper()
	if !swarm.SQLiteOnPath() {
		t.Skipf("%s is not on PATH, and a numeric budget is refused without it (rule 13d)", swarm.SQLiteBinary)
	}
}

// startRun runs one card through nativeRun with the start waits recorded, not slept.
func startRun(t *testing.T, label, card string) (nativeRunResult, string, []time.Duration, string) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	var waits []time.Duration
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label, card: []byte(card),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
		startSleep: func(d time.Duration) { waits = append(waits, d) },
	}, &errOut)
	require.Equal(t, 0, code, "%s", errOut.String())
	return res, errOut.String(), waits, filepath.Join(slot, "jobs", label)
}

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
	require.NoError(t, os.MkdirAll(b.pool, 0o755))
	write(t, filepath.Join(b.pool, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	// The three binaries are built ONCE for the whole package, not once per bench. Thirty
	// benches building them each saturated the machine, and a dispatcher that cannot start
	// a child inside its deadline turns a contract test into a race: three tests that kill
	// a worker went red under the load and green on their own (2026-09-11).
	b.binary, b.path = builtBinaries(t)
	b.sandbox, b.fakeSandbox = builtSandbox, builtFakeSandbox

	home := filepath.Join(dir, "worker-home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	write(t, filepath.Join(home, "AGENTS.md"), "the worker's own self, copied one way into every slot\n")

	b.keyFile = filepath.Join(dir, "key")
	write(t, b.keyFile, "FAKE_KEY="+fakeKey+"\na second line nothing may read\n")
	require.NoError(t, os.Chmod(b.keyFile, 0o600))

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
	err := buildShared()
	require.NoError(t, err, "building the binaries these tests run: %v", err)
	return builtTool, builtPath
}

// swarm runs the built binary, which is what a stranger meets at a shell prompt. It fails
// the test on a binary that would not run at all, so it belongs to the TEST GOROUTINE:
// anything running beside the test calls swarmTry and hands the answer back.
func (b *bench) swarm(args ...string) (exit int, stdout, stderr string) {
	b.t.Helper()
	exit, stdout, stderr, err := b.swarmTry(args...)
	if err != nil {
		b.t.Fatalf("running nova-worker %s: %v", strings.Join(args, " "), err)
	}
	return exit, stdout, stderr
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

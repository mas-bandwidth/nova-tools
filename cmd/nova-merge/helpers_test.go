package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The fixture every contract test runs against: a BARE GIT REPOSITORY in t.TempDir() and
// a fake host. Nothing here reaches the network, nothing is written outside t.TempDir(),
// and the git is real -- the lease, the fast-forward and the two-parent merge are
// properties of git and a fake git would prove none of them.

// TestMain gives every test in this package THE ENVIRONMENT A CI RUNNER HAS: a git with
// no identity anywhere -- no global config, no system config, no ambient EMAIL, and
// user.useConfigOnly so git may not guess one from the account either.
//
// On a laptop git guesses a name from the account and every commit the tool writes works
// by accident; on the Ubuntu leg of #57 it could not, and `git merge --no-ff` died with
// "Committer identity unknown". The fixture's own commits pass their identity explicitly
// (see lab.git), so what is left under test is whether THE TOOL carries its own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nova-merge-gitconfig")
	if err != nil {
		panic(err)
	}
	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		panic(err)
	}
	os.Setenv("GIT_CONFIG_GLOBAL", cfg)
	os.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "no-such-gitconfig"))
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"EMAIL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		os.Unsetenv(name)
	}
	// The lab fixture newLab copies is built under this directory too, so it is
	// removed with it.
	labFixtureRoot = dir
	// THE LOCK CLOCK IS INJECTED. merge.Lock's bounded wait is a deadline read from a
	// clock and a sleep between polls, and a test that must exercise a verb's
	// --timeout would otherwise hold the machine's clock for those seconds -- which is
	// exactly the wall time the slowtests budget refuses. Every test in this package
	// drives the same process, so one locked clock stands in for the real one; only
	// the lock wait reads it.
	injectLockClock()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// lockClock is the injected clock merge.Lock reads: a mutex-guarded instant that Sleep
// advances, so a wait of seconds runs to its end in a few hundred iterations and no
// test's elapsed time carries the deadline. It is safe for the package's parallel tests
// because every advance and read is serialised here.
type lockClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *lockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *lockClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// injectLockClock points the merge package's two wait seams at one fake clock for the
// whole package run. The instant is the tests' own fixed instant, so nothing in a lock
// refusal depends on the machine's time either. The clock is kept in lockClk so a
// timeout test can assert how far the wait advanced without reading the wall clock.
func injectLockClock() {
	lockClk = &lockClock{at: time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)}
	merge.Now = lockClk.Now
	merge.Sleep = lockClk.Sleep
}

// lockClk is the fake clock the lock wait reads, for a test that asserts on how long the
// verb waited: the wait is measured in injected time, never in wall time.
var lockClk *lockClock

// realLockClock puts the merge package's two wait seams back on the MACHINE's clock for
// the rest of one test, and restores the injected one when that test ends.
//
// The injected clock is one instant shared by the whole process and every waiter's poll
// advances it. That is what a timeout test wants -- a bounded wait runs to its end with
// no wall time -- but it is wrong for the one test whose writers must really wait on each
// other: three waiters polling a held lock advance the shared instant by three poll
// intervals per round, so the whole 120 s bound passes in a few hundred real
// microseconds and every waiter but the first is refused before the holder has finished
// its write. On a fast, idle machine the holder wins that race and the test passes; on a
// loaded one it does not, which is a test that asserts the machine (#1206 was green on CI
// and red on the Studio the CI runners share).
//
// Only a test that does not call t.Parallel may use it: Go resumes the paused parallel
// tests after the sequential ones have finished, so nothing else is reading the clock
// while such a test runs.
func realLockClock(t *testing.T) {
	t.Helper()
	merge.Now = time.Now
	merge.Sleep = time.Sleep
	t.Cleanup(injectLockClock)
}

type lab struct {
	t      *testing.T
	dir    string
	remote string // the bare repository, which is the fake forge
	work   string // a clone the test uses to make commits
	lane   string
	host   *merge.FakeHost
	// queue, when set, is what a `simulate` run with no --entries reads; it is the fake
	// gh of these tests, and it reaches nothing.
	queue QueueReader
	// launcher is the fake the rebase verb's cards are handed to, so a test proves the
	// launch without a bench.
	launcher *fakeLauncher
	now      time.Time
	build    string
	runner   merge.Runner
	// urlFor, when set, is what RepoURL answers -- so a test can point init at a
	// repository that is not there.
	urlFor func(string) string
	// heads is the batch fixture's pull request heads by number, so a batch test can
	// say what the FORGE thinks of one member's own head (edge 25).
	heads map[int]string
}

func newLab(t *testing.T) *lab {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine; every test here drives a real git against a bare fixture repository")
	}
	dir := t.TempDir()
	l := &lab{
		t: t, dir: dir,
		remote:   filepath.Join(dir, "remote.git"),
		work:     filepath.Join(dir, "work"),
		lane:     filepath.Join(dir, "lane"),
		host:     merge.NewFakeHost(),
		launcher: &fakeLauncher{},
		now:      time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC),
		build:    "aaaaaaaaaaaa",
	}
	// The bare repository, its first commit and the clone are BUILT ONCE for the
	// process and COPIED here. Building them is six git subprocesses, and 82 newLab
	// calls in this package made that roughly five hundred git spawns and 98 s of
	// `go test ./cmd/nova-merge/` (#516, Glenn's two-minute rule). The copy hands out
	// the same bytes -- same first commit, same update hook -- and each test then owns
	// its copy outright: it pushes, merges and corrupts it with nothing shared.
	labFixtureOnce.Do(func() { labFixtureDir, labFixtureErr = buildLabFixture() })
	if labFixtureErr != nil {
		t.Fatalf("the lab fixture: %v", labFixtureErr)
	}
	if err := os.CopyFS(dir, os.DirFS(labFixtureDir)); err != nil {
		t.Fatalf("copying the lab fixture: %v", err)
	}
	// The copied clone still names the TEMPLATE's bare repository; origin has to be
	// this test's own copy or a push would reach the fixture every other test reads.
	l.git(l.work, "remote", "set-url", "origin", l.remote)
	return l
}

// The process-wide fixture newLab copies: a `remote.git` with one commit on main and
// its `work` clone. Built under TestMain's directory and removed with it.
var (
	labFixtureOnce sync.Once
	labFixtureDir  string
	labFixtureErr  error
	labFixtureRoot string // set by TestMain, the parent the fixture is built under
)

// buildLabFixture builds that template with the same git calls newLab used to make per
// test. It takes no *testing.T: it runs under sync.Once, where the caller that loses
// the race is not the test whose failure it would be.
func buildLabFixture() (string, error) {
	dir, err := os.MkdirTemp(labFixtureRoot, "merge-fixture-")
	if err != nil {
		return "", err
	}
	remote, work := filepath.Join(dir, "remote.git"), filepath.Join(dir, "work")
	var fail error
	git := func(at string, args ...string) {
		if fail != nil {
			return
		}
		cmd := exec.Command("git", merge.NoBackgroundGit(args...)...)
		cmd.Dir = at
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@localhost",
			"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@localhost",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			fail = fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git(dir, "init", "--bare", "-b", "main", remote)
	git(dir, "clone", remote, work)
	// THE SETTINGS GO IN THE REPOSITORIES THEMSELVES, not only on the fixture's own
	// command lines. A push to a path runs `git receive-pack <path>` in the OTHER
	// repository, and git clears the -c settings out of the environment before it starts
	// a git on a repository that is not this one (local_repo_env) -- so the remote's
	// receive-pack forks its own detached `maintenance run --auto` unless the remote's
	// own config says not to. Both repositories are copied into each test's t.TempDir,
	// and they carry this with them (#1607).
	for _, at := range []string{remote, work} {
		for _, kv := range quietRepoSettings() {
			git(at, "config", kv[0], kv[1])
		}
	}
	if fail == nil {
		fail = os.WriteFile(filepath.Join(work, "README.md"), []byte("the fixture\n"), 0o644)
	}
	git(work, "add", "-A")
	git(work, "-c", "user.name=fixture", "-c", "user.email=fixture@localhost", "commit", "-q", "-m", "the first commit")
	git(work, "push", "origin", "HEAD:refs/heads/main")
	// The update hook records every push the remote received, so a test can assert that
	// the base only ever moved forward by exactly one commit (demanded test 4).
	if fail == nil {
		fail = os.WriteFile(filepath.Join(remote, "hooks", "update"),
			[]byte("#!/bin/sh\necho \"$1 $2 $3\" >> \"$GIT_DIR/pushes\"\n"), 0o755)
	}
	if fail != nil {
		return "", fail
	}
	return dir, nil
}

// quietRepoSettings is merge.NoBackgroundGit's list as key/value pairs, for writing INTO a
// fixture repository rather than onto one command line.
//
// A push to a path runs `git receive-pack <path>` in the OTHER repository, and git clears
// these settings out of the environment before it starts a git on a repository that is not
// this one (local_repo_env) -- so a repository this fixture pushes to has to carry them
// itself, or its receive-pack forks the detached `maintenance run --auto` that #1607 is.
func quietRepoSettings() [][2]string {
	var out [][2]string
	settings := merge.NoBackgroundGit()
	for i := 0; i+1 < len(settings); i += 2 {
		key, value, _ := strings.Cut(settings[i+1], "=")
		out = append(out, [2]string{key, value})
	}
	return out
}

// git runs one git in the fixture, and it runs it THE WAY THE TOOL RUNS GIT: through
// merge.NoBackgroundGit, so no `git maintenance run --auto --detach` is left writing into
// a repository that lives in t.TempDir and is about to be removed under it (#1607).
func (l *lab) git(dir string, args ...string) string {
	l.t.Helper()
	cmd := exec.Command("git", merge.NoBackgroundGit(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@localhost",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@localhost",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		l.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (l *lab) write(name, body string) {
	l.t.Helper()
	full := filepath.Join(l.work, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) commit(message string) string {
	l.t.Helper()
	l.git(l.work, "add", "-A")
	l.git(l.work, "-c", "user.name=fixture", "-c", "user.email=fixture@localhost", "commit", "-q", "-m", message)
	return l.git(l.work, "rev-parse", "HEAD")
}

// branch makes a branch at the remote holding one new commit, and returns its sha. It is
// how a test makes an entry the lane can really merge.
func (l *lab) branch(name, file, body, message string) string {
	l.t.Helper()
	l.git(l.work, "checkout", "-q", "-B", name, "origin/main")
	l.write(file, body)
	oid := l.commit(message)
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/"+name)
	l.git(l.work, "checkout", "-q", "main")
	return oid
}

func (l *lab) baseSHA() string {
	l.t.Helper()
	return l.git(l.work, "rev-parse", "refs/remotes/origin/main")
}

func (l *lab) refreshBase() string {
	l.t.Helper()
	l.git(l.work, "fetch", "-q", "origin", "main")
	return l.git(l.work, "rev-parse", "FETCH_HEAD")
}

// hookRunner is how a test puts A HAND AT ANOTHER KEYBOARD inside the window rule 21
// closes: it runs before the command the lane is about to run, so a test can move the
// remote's base between the pass's last read and its push and prove the lease catches it.
type hookRunner struct {
	inner  merge.Runner
	before func(dir string, args []string)
}

func (h *hookRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if h.before != nil {
		h.before(dir, args)
	}
	return h.inner.Run(ctx, dir, name, args...)
}

// beforePush installs a hand that runs once, immediately before the lane's lease push.
func (l *lab) beforePush(hand func()) {
	done := false
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(_ string, args []string) {
		if done || !isLeasePush(args) {
			return
		}
		done = true
		hand()
	}}
}

// afterPush installs a hand that runs once in the window BETWEEN the lease landing and the
// read-back of rule 21 -- which is the next fetch of the base. It is how a test reaches
// `MERGE FAIL ... published object not at base` on the push branch.
func (l *lab) afterPush(hand func()) {
	pushed, done := false, false
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(_ string, args []string) {
		if isLeasePush(args) {
			pushed = true
			return
		}
		if !pushed || done || len(args) == 0 || args[0] != "fetch" {
			return
		}
		done = true
		hand()
	}}
}

func isLeasePush(args []string) bool {
	if len(args) == 0 || args[0] != "push" {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--force-with-lease=") {
			return true
		}
	}
	return false
}

func (l *lab) deps() Deps {
	return Deps{
		Runner: l.runner,
		Now:    func() time.Time { return l.now },
		Sleep:  func(d time.Duration) { l.now = l.now.Add(d) },
		RepoURL: func(repo string) string {
			if l.urlFor != nil {
				return l.urlFor(repo)
			}
			return l.remote
		},
		NewHost: func(string, time.Duration) merge.Host { return l.host },
		NewQueue: func(string, time.Duration) QueueReader {
			if l.queue != nil {
				return l.queue
			}
			return nil
		},
		NewRebaseList: func(string, time.Duration) merge.RebaseList { return l.host },
		Launcher:      l.launcher,
		BuildID:       func() string { return l.build },
		// react's two edges. Dial is the caller's own address -- every react test
		// hands it a miniredis of its own -- and the forge is the fake.
		BatchGate: labBatchGate(),
		Dial:      func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(string, string, time.Duration) ci.Forge {
			return &reactFakeForge{}
		},
	}
}

// labBatchGate is the landing gate's suite AS THE TESTS RUN IT: batchGate with the cross
// vet taken out, and nothing else taken out.
//
// The gate a friend runs is batchGate, and these tests run the rest of it for real -- the
// build, the vet, CI's own `go test -json` command and the lisp suite. The one step that
// cannot be paid here is `vet-windows`, because `GOOS=windows go vet ./...` must build the
// WINDOWS STANDARD LIBRARY into the cache first: seconds on an idle 64-core bench and far
// more on a darwin runner sharing its machine with seven others. Paid inside `go test`, it
// comes out of this package's own -timeout, and the package's serial tests are what its
// parallel tests wait behind -- which is exactly how the merge group's darwin leg reached
// `panic: test timed out after 1m40s` with ten parallel tests starved at 14 s each on
// 2026-09-18.
//
// TestTheGateCrossVetsForWindows pins the step in the real list and pins that this list
// differs from it by that one name and no other, so the exemption cannot quietly grow.
func labBatchGate() []batchStep {
	out := make([]batchStep, 0, len(batchGate))
	for _, step := range batchGate {
		if step.name == crossVetStep {
			continue
		}
		out = append(out, step)
	}
	return out
}

// run drives the binary's own run() with the test's deps, which is what a stranger's
// shell reaches.
func (l *lab) run(args ...string) (int, string, string) {
	l.t.Helper()
	effective := append([]string(nil), args...)
	if len(effective) > 0 && effective[0] == "batch" {
		hasReviewers := false
		hasNoRequire := false
		hasLane := false
		for _, a := range effective {
			if a == "--reviewers" || strings.HasPrefix(a, "--reviewers=") {
				hasReviewers = true
			}
			if a == "--no-require-holds" {
				hasNoRequire = true
			}
			if a == "--lane" || strings.HasPrefix(a, "--lane=") {
				hasLane = true
			}
		}
		if !hasReviewers && !hasNoRequire {
			effective = append(effective, "--no-require-holds", "--reason", "test")
		}
		if hasReviewers && !hasLane && l.lane != "" {
			_ = os.MkdirAll(l.lane, 0755)
			effective = append(effective, "--lane", l.lane)
		}
	}
	var out, errb bytes.Buffer
	exit := run(effective, &out, &errb, l.deps())
	return exit, out.String(), errb.String()
}

func (l *lab) runBare(args ...string) (int, string, string) {
	l.t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, l.deps())
	return exit, out.String(), errb.String()
}

// init makes the lane. Every test starts here, because init is the only verb that creates
// one.
func (l *lab) init(base string) {
	l.t.Helper()
	exit, _, errb := l.run("init", "--lane", l.lane, "--repo", "o/n", "--base", base, "--lane-branch", "nova-merge/lane")
	if exit != 0 {
		l.t.Fatalf("init: exit %d\n%s", exit, errb)
	}
}

// summary writes a gate summary file, which `gate` requires to exist.
func (l *lab) summary(body string) string {
	l.t.Helper()
	// The name is made of the body, and a body carrying a timestamp carries colons, which
	// Windows does not allow in a file name at all (it reads them as a stream separator).
	// Every character a path may not hold becomes a dash here, on every platform, so the
	// fixture writes the same name everywhere.
	path := filepath.Join(l.dir, "summary-"+strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, body)+".txt")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		l.t.Fatal(err)
	}
	return path
}

// pushes is what the remote's update hook recorded: one line per ref it was asked to
// move, old and new.
func (l *lab) pushes() []string {
	raw, err := os.ReadFile(filepath.Join(l.remote, "pushes"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// mergeSHAOf reads the integration commit `run` built for an entry out of RUN BUILT.
func mergeSHAOf(t *testing.T, stdout, entry string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "RUN BUILT ") || !strings.Contains(line, "entry="+entry+" ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "ref=") {
				parts := strings.Split(tok, "/")
				return parts[len(parts)-1]
			}
		}
	}
	t.Fatalf("no RUN BUILT line for entry %s in:\n%s", entry, stdout)
	return ""
}

func contains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("want %q in:\n%s", needle, haystack)
	}
}

func absent(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("did not want %q in:\n%s", needle, haystack)
	}
}

// timeValue and parseStamp let a test move the clock to an exact instant, which is what
// the tie tests need: two records with ONE `at` to the second.
type timeValue = time.Time

func parseStamp(s string) time.Time {
	t, err := time.Parse(merge.Stamp, s)
	if err != nil {
		panic(err)
	}
	return t
}

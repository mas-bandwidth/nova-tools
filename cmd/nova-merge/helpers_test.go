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
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type lab struct {
	t      *testing.T
	dir    string
	remote string // the bare repository, which is the fake forge
	work   string // a clone the test uses to make commits
	lane   string
	host   *merge.FakeHost
	now    time.Time
	build  string
	runner merge.Runner
	// urlFor, when set, is what RepoURL answers -- so a test can point init at a
	// repository that is not there.
	urlFor func(string) string
}

func newLab(t *testing.T) *lab {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine; every test here drives a real git against a bare fixture repository")
	}
	dir := t.TempDir()
	l := &lab{
		t: t, dir: dir,
		remote: filepath.Join(dir, "remote.git"),
		work:   filepath.Join(dir, "work"),
		lane:   filepath.Join(dir, "lane"),
		host:   merge.NewFakeHost(),
		now:    time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC),
		build:  "aaaaaaaaaaaa",
	}
	l.git(dir, "init", "--bare", "-b", "main", l.remote)
	l.git(dir, "clone", l.remote, l.work)
	l.write("README.md", "the fixture\n")
	l.commit("the first commit")
	l.git(l.work, "push", "origin", "HEAD:refs/heads/main")
	// The update hook records every push the remote received, so a test can assert that
	// the base only ever moved forward by exactly one commit (demanded test 4).
	hook := filepath.Join(l.remote, "hooks", "update")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho \"$1 $2 $3\" >> \"$GIT_DIR/pushes\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *lab) git(dir string, args ...string) string {
	l.t.Helper()
	cmd := exec.Command("git", args...)
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
		BuildID: func() string { return l.build },
	}
}

// run drives the binary's own run() with the test's deps, which is what a stranger's
// shell reaches.
func (l *lab) run(args ...string) (int, string, string) {
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
	path := filepath.Join(l.dir, "summary-"+strings.ReplaceAll(body, "/", "-")+".txt")
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

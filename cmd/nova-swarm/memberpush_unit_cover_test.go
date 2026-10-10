package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The member's push (memberpush.go) is unit tested here against a scripted fake git: no
// process is started, no clock is read and no socket is opened. The real-git paths that
// need a checkout, an *exec.ExitError with code 1, or a real `gh` stay with the slow and
// functional tiers (slow_test.go, memberpush_cover_test.go), and the report says so.

// The fake git answers by args[0]: init, config, fetch, rev-parse, for-each-ref,
// merge-base, rev-list, push and update-ref. Every field is a scripted answer a test sets;
// the zero value is "git said yes and said nothing", which is what a passing call gets.
type fakeMemberGit struct {
	mu    sync.Mutex
	argv  [][]string
	opts  []gitrun.Options
	stdin []string

	fetchCalls int
	pushCalls  int

	initErr      error
	configFail   string // a `config` key whose set fails, "" for none
	fetchFail    bool   // every fetch fails
	fetchLine    string // the stderr line a failing fetch reports
	verifyFail   bool   // the rev-parse of the result's head fails
	containsRefs string // for-each-ref --contains <head> stdout
	tips         string // for-each-ref %(objectname) stdout (checkoutTip)
	dropRefs     string // for-each-ref --format=delete stdout (drop)
	mergeBaseErr error  // a merge-base that could not answer
	count        string // rev-list --count stdout
	pushFail     int    // the first n pushes fail
	pushLine     string // the porcelain stdout of a failing push
}

func (f *fakeMemberGit) run(_ context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opts = append(f.opts, o)
	f.argv = append(f.argv, slices.Clone(args))
	stdin := ""
	if o.Stdin != nil {
		b, err := io.ReadAll(o.Stdin)
		if err != nil {
			return gitrun.Result{}, err
		}
		stdin = string(b)
	}
	f.stdin = append(f.stdin, stdin)

	switch args[0] {
	case "init":
		if f.initErr != nil {
			return gitrun.Result{Stderr: []byte("fatal: could not create\n")}, f.initErr
		}
	case "config":
		if f.configFail != "" && len(args) >= 3 && args[2] == f.configFail {
			return gitrun.Result{Stderr: []byte("fatal: config failed\n")}, assert.AnError
		}
	case "fetch":
		f.fetchCalls++
		if f.fetchFail {
			line := f.fetchLine
			if line == "" {
				line = "fatal: bad object"
			}
			return gitrun.Result{Stderr: []byte(line + "\n")}, assert.AnError
		}
	case "rev-parse":
		if f.verifyFail {
			return gitrun.Result{Stderr: []byte("fatal: needed a single revision\n")}, assert.AnError
		}
		head := strings.TrimSuffix(args[len(args)-1], "^{commit}")
		return gitrun.Result{Stdout: []byte(head + "\n")}, nil
	case "for-each-ref":
		for _, a := range args {
			switch {
			case strings.Contains(a, "delete"):
				return gitrun.Result{Stdout: []byte(f.dropRefs)}, nil
			case strings.Contains(a, "%(objectname)"):
				return gitrun.Result{Stdout: []byte(f.tips)}, nil
			}
		}
		return gitrun.Result{Stdout: []byte(f.containsRefs)}, nil
	case "merge-base":
		if f.mergeBaseErr != nil {
			return gitrun.Result{Stderr: []byte("fatal: could not say\n")}, f.mergeBaseErr
		}
	case "rev-list":
		return gitrun.Result{Stdout: []byte(f.count + "\n")}, nil
	case "push":
		f.pushCalls++
		if f.pushCalls <= f.pushFail {
			return gitrun.Result{Stdout: []byte(f.pushLine)}, assert.AnError
		}
	}
	return gitrun.Result{}, nil
}

// calls is how many gits the fake has run.
func (f *fakeMemberGit) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.argv)
}

// countArgv is how many times a verb was run.
func (f *fakeMemberGit) countArgv(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.argv {
		if len(a) > 0 && a[0] == verb {
			n++
		}
	}
	return n
}

// pushArgv is the argv of the last push, or nil.
func (f *fakeMemberGit) pushArgv() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.argv) - 1; i >= 0; i-- {
		if len(f.argv[i]) > 0 && f.argv[i][0] == "push" {
			return slices.Clone(f.argv[i])
		}
	}
	return nil
}

// lastOpts is the Options of the last git run.
func (f *fakeMemberGit) lastOpts() gitrun.Options {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.opts) == 0 {
		return gitrun.Options{}
	}
	return f.opts[len(f.opts)-1]
}

// stdinFor is the standard input recorded for every run of one verb.
func (f *fakeMemberGit) stdinFor(verb string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for i, a := range f.argv {
		if len(a) > 0 && a[0] == verb {
			out = append(out, f.stdin[i])
		}
	}
	return out
}

const (
	coverBase = "1111111111111111111111111111111111111111"
	coverHead = "2222222222222222222222222222222222222222"
	coverTip  = "3333333333333333333333333333333333333333"
	coverTip2 = "4444444444444444444444444444444444444444"
)

// memberPushCoverRig is one gitPusher over the fake git, a recording sleep and a notes
// buffer, with the card's checkout and staged file under one t.TempDir().
type memberPushCoverRig struct {
	root, slots string
	p           member.Packet
	git         *fakeMemberGit
	sleeps      []time.Duration
	notes       bytes.Buffer
	g           *gitPusher
}

func newMemberPushCoverRig(t *testing.T) *memberPushCoverRig {
	t.Helper()
	root := t.TempDir()
	r := &memberPushCoverRig{
		root:  root,
		slots: filepath.Join(root, "slots"),
		git:   &fakeMemberGit{count: "1"},
		p: member.Packet{
			Card: "c1", Kind: "work", As: "m1", Primary: "p1", Stream: "s1",
			Attempt: 1, Gen: 1, Epoch: 7, Branch: "sprint/c1",
			Brief: "REPO: o/r\nBASE: main\n\nDo the work.\n",
		},
	}
	r.g = &gitPusher{
		root:  root,
		slots: r.slots,
		git:   r.git.run,
		sleep: func(d time.Duration) { r.sleeps = append(r.sleeps, d) },
		notes: &r.notes,
	}
	return r
}

func (r *memberPushCoverRig) launchDir() string {
	return filepath.Join(r.slots, launchName(r.p))
}

// checkout makes the checkout the push reads a staged commit from, with the .git
// directory the code only stats.
func (r *memberPushCoverRig) checkout(t *testing.T) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(r.launchDir(), "jobs", r.p.Card, swarm.JobRepo, ".git"), 0o755))
}

// pushRepo makes the member's push repository look already built, so repo() runs no git.
func (r *memberPushCoverRig) pushRepo(t *testing.T) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(r.root, "push.git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(r.root, "push.git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
}

// staged records the commit native staged the launch at.
func (r *memberPushCoverRig) staged(t *testing.T, sha string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(r.launchDir(), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(r.launchDir(), cardcontract.StagedName), []byte(sha+"\n"), 0o644))
}

// landed makes the fake answer every check of a push of coverHead with a yes: the head is
// a commit of the checkout, it descends from the staged commit, and the child made one
// commit of its own.
func (r *memberPushCoverRig) landed(t *testing.T) {
	t.Helper()
	r.checkout(t)
	r.pushRepo(t)
	r.staged(t, coverBase)
	r.git.containsRefs = "refs/member/c1.g1.e7/HEAD\n"
}

func TestSwarmMemberpushCoverRefusesHeadNotSha(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	got := r.g.Push(r.p, member.Result{Head: "not-a-sha"})
	assert.Contains(t, got.Refused, "is not a sha")
	assert.Empty(t, got.Sha)
	assert.Empty(t, got.None)
	assert.Zero(t, r.git.calls(), "no git runs for a head that is not a sha")
}

func TestSwarmMemberpushCoverRefusesNotABranchName(t *testing.T) {
	t.Parallel()

	for _, branch := range []string{"a..b", "x.lock", "+main"} {
		t.Run(branch, func(t *testing.T) {
			t.Parallel()
			r := newMemberPushCoverRig(t)
			r.p.Branch = branch
			got := r.g.Push(r.p, member.Result{Head: coverHead})
			assert.Contains(t, got.Refused, "is not a branch name")
			assert.Zero(t, r.git.calls(), "no git runs for a branch name git would read as a refspec")
		})
	}
}

func TestSwarmMemberpushCoverNoRepository(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.p.Brief = "a card with no repository header\n"
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Equal(t, "the card names no repository", got.None)
	assert.Zero(t, r.git.calls())
}

func TestSwarmMemberpushCoverNoCheckout(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	want := filepath.Join(r.slots, launchName(r.p), "jobs", r.p.Card, swarm.JobRepo)
	assert.Contains(t, got.Refused, "no checkout at "+want)
	assert.Zero(t, r.git.calls())
}

func TestSwarmMemberpushCoverInitFails(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.git.initErr = errors.New("init failed")
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.Refused, "git init")
}

func TestSwarmMemberpushCoverFetchFailsEveryTry(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.git.fetchFail = true
	r.git.fetchLine = "fatal: bad object refs/member/other/HEAD"
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.Refused, "4 tries")
	assert.Contains(t, got.Refused, "bad object")
	assert.Equal(t, 4, r.git.fetchCalls)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, r.sleeps)
}

func TestSwarmMemberpushCoverHeadNotACommitNoStaged(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.git.verifyFail = true
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.Refused, "is not a commit on the checkout's branches")
}

func TestSwarmMemberpushCoverHeadOnRefsNoStaged(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.git.containsRefs = "refs/member/c1.g1.e7/HEAD\n"
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.None, "no staged commit is recorded")
}

func TestSwarmMemberpushCoverHeadNotDescend(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.staged(t, coverBase)
	r.git.containsRefs = "refs/member/c1.g1.e7/HEAD\n"
	r.git.mergeBaseErr = errors.New("not a descendant")
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.Refused, "does not descend from the staged commit")
}

func TestSwarmMemberpushCoverZeroCommits(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.landed(t)
	r.git.count = "0"
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.None, "committed nothing")
}

func TestSwarmMemberpushCoverPushRemoteRejectedThenAccepted(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.landed(t)
	r.git.pushFail = 2
	r.git.pushLine = "!\t" + coverHead + ":refs/heads/sprint/c1\t[remote rejected] (failed)\n"
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Equal(t, coverHead, got.Sha)
	assert.Empty(t, got.Refused)
	assert.Equal(t, 3, r.git.pushCalls)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, r.sleeps)
}

func TestSwarmMemberpushCoverPushRefusedPorcelain(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.landed(t)
	r.git.pushFail = 1
	r.git.pushLine = "!\t" + coverHead + ":refs/heads/sprint/c1\t[rejected] (fetch first)\n"
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Contains(t, got.Refused, "[rejected]")
	assert.Equal(t, 1, r.git.pushCalls, "a non-remote rejection is refused at once")
	assert.Empty(t, r.sleeps, "a refused push is never sent again")
}

func TestSwarmMemberpushCoverLanding(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.landed(t)
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Equal(t, coverHead, got.Sha)
	assert.Empty(t, got.PR, "a result with no title opens no pull request")
	assert.Empty(t, got.PRNote)
	push := r.git.pushArgv()
	require.NotNil(t, push, "the push ran")
	assert.Equal(t, coverHead+":refs/heads/sprint/c1", push[len(push)-1])
	assert.Contains(t, push, "--no-verify")
}

func TestSwarmMemberpushCoverCheckoutTipOne(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.staged(t, coverBase)
	r.git.verifyFail = true
	r.git.tips = coverTip
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.Equal(t, coverTip, got.Sha, "the checkout's one descendant tip is pushed")
	assert.Equal(t, 1, strings.Count(r.notes.String(), "NOTE "), "exactly one NOTE line")
	assert.Contains(t, r.notes.String(), coverHead, "the NOTE names the head the result claimed")
	assert.Contains(t, r.notes.String(), coverTip, "the NOTE names the head that was pushed")
}

func TestSwarmMemberpushCoverCheckoutTipTwo(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.staged(t, coverBase)
	r.git.verifyFail = true
	r.git.tips = coverTip + " " + coverTip2
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.NotEmpty(t, got.Refused, "two lines of work are no one line to push")
	assert.Empty(t, got.Sha)
}

func TestSwarmMemberpushCoverCheckoutTipMergeBaseError(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.checkout(t)
	r.pushRepo(t)
	r.staged(t, coverBase)
	r.git.verifyFail = true
	r.git.tips = coverTip
	r.git.mergeBaseErr = errors.New("git could not say")
	got := r.g.Push(r.p, member.Result{Head: coverHead})
	assert.NotEmpty(t, got.Refused, "a tip that was not judged is never pushed")
	assert.Empty(t, got.Sha)
}

func TestSwarmMemberpushCoverCardRepoFrame(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	require.NoError(t, os.MkdirAll(r.launchDir(), 0o755))
	frame := cardcontract.Frame{Kind: "work", Card: r.p.Card, Attempt: 1, Branch: r.p.Branch, Repo: "o/r", BaseRef: "trunk"}
	require.NoError(t, cardcontract.WriteFrame(r.launchDir()+cardcontract.FrameName, frame))
	url, ref := r.g.cardRepo(r.p)
	assert.True(t, strings.HasPrefix(url, "https://"), "the frame's repo is a clone URL, got %q", url)
	assert.True(t, strings.HasSuffix(url, "/o/r.git"), "the frame's repo, as its clone URL, got %q", url)
	assert.Equal(t, "trunk", ref)
}

func TestSwarmMemberpushCoverCardRepoBrief(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	url, ref := r.g.cardRepo(r.p)
	assert.True(t, strings.HasPrefix(url, "https://"), "the brief's REPO header is a clone URL, got %q", url)
	assert.True(t, strings.HasSuffix(url, "/o/r.git"), "the brief's REPO header, as its clone URL, got %q", url)
	assert.Equal(t, "main", ref)
}

func TestSwarmMemberpushCoverDropStdin(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	refs := "delete refs/member/c1.g1.e7/HEAD\ndelete refs/member/c1.g1.e7/heads/c1\n"
	r.git.dropRefs = refs
	r.g.drop(context.Background(), filepath.Join(r.root, "push.git"), "refs/member/c1.g1.e7")
	got := r.git.stdinFor("update-ref")
	require.Len(t, got, 1, "one update-ref --stdin")
	assert.Equal(t, refs, got[0], "the for-each-ref output is the update-ref stdin")
}

func TestSwarmMemberpushCoverDropEmpty(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.git.dropRefs = ""
	r.g.drop(context.Background(), filepath.Join(r.root, "push.git"), "refs/member/c1.g1.e7")
	assert.Zero(t, r.git.countArgv("update-ref"), "nothing to delete runs no update-ref")
}

func TestSwarmMemberpushCoverRunEnv(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.g.env = []string{"FOO=bar"}
	_, err := r.g.run(context.Background(), "push.git", nil, "for-each-ref")
	require.NoError(t, err)
	o := r.git.lastOpts()
	assert.Equal(t, "push.git", o.C)
	assert.Contains(t, o.Env, "FOO=bar")
	assert.Contains(t, o.Env, "GIT_TERMINAL_PROMPT=0")
}

func TestSwarmMemberpushCoverRepoInit(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	repo, err := r.g.repo()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(r.root, "push.git"), repo)
	assert.Equal(t, 1, r.git.countArgv("init"))
	assert.Equal(t, 3, r.git.countArgv("config"), "gc.auto, maintenance.auto and core.hooksPath")
}

func TestSwarmMemberpushCoverRepoConfigFail(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.git.configFail = "gc.auto"
	_, err := r.g.repo()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git config gc.auto", "the error names the key that failed")
}

func TestSwarmMemberpushCoverRepoExisting(t *testing.T) {
	t.Parallel()

	r := newMemberPushCoverRig(t)
	r.pushRepo(t)
	repo, err := r.g.repo()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(r.root, "push.git"), repo)
	assert.Zero(t, r.git.calls(), "a push repository already made runs no git")
}

func TestSwarmMemberpushCoverGitLine(t *testing.T) {
	t.Parallel()

	fail := errors.New("git failed")
	for _, tc := range []struct {
		name string
		res  gitrun.Result
		err  error
		want string
	}{
		{name: "porcelain ref line", res: gitrun.Result{Stdout: []byte("To origin\n!\tmain:refs/heads/main\t[rejected]\nDone\n")}, err: fail, want: "! main:refs/heads/main [rejected]"},
		{name: "fatal line", res: gitrun.Result{Stderr: []byte("warning: x\nfatal: bad object\nmore\n")}, err: fail, want: "fatal: bad object"},
		{name: "error line", res: gitrun.Result{Stderr: []byte("warning: x\nerror: y\n")}, err: fail, want: "error: y"},
		{name: "last stderr line", res: gitrun.Result{Stderr: []byte("first\nlast line\n")}, err: fail, want: "last line"},
		{name: "the error only", res: gitrun.Result{}, err: fail, want: fail.Error()},
		{name: "nothing", res: gitrun.Result{}, err: nil, want: "git failed and said nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, gitLine(tc.res, tc.err))
		})
	}
}

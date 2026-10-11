package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier of (g *gitPusher) Push (memberpush.go:88): its contract is the type
// comment (memberpush.go:26-51; docs/SPEC-SWARM.md, member; docs/SPEC-CARD-CONTRACT.md
// section 4; tla/CardContract.tla, Push). Every test drives Push with a git that answers
// by argv, records each call with its directory and env, a sleep that records the waits,
// and gh off PATH, so no git, no clock and no child is real. The slow tier owns the real
// git; the checkoutTip not-a-descendant branch needs the *exec.ExitError a fake cannot
// build, so it is left to slow_test.go and named in the report.

// scriptedGit is a git that answers by argv and records every call's options. Its
// fields are the answers: the full sha rev-parse returns, whether the head is contained
// by the launch's refs, whether it descends from the staged commit, the rev-list count,
// and how many fetches and pushes fail before the rest succeed.
type scriptedGit struct {
	mu    sync.Mutex
	calls []gitrun.Options
	argv  [][]string

	initErr  bool
	initLine string

	fetchFail int
	fetches   int
	fetchLine string

	pushFail int
	pushes   int
	pushLine string

	head          string
	contains      string
	notDescendant bool
	commits       string
}

func (s *scriptedGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, o)
	s.argv = append(s.argv, slices.Clone(args))
	s.mu.Unlock()
	switch args[0] {
	case "init":
		if s.initErr {
			line := s.initLine
			if line == "" {
				line = "fatal: could not create the push repository"
			}
			return gitrun.Result{Stderr: []byte(line + "\n")}, errors.New("exit status 128")
		}
		repo := args[len(args)-1]
		if err := os.MkdirAll(repo, 0o755); err != nil {
			return gitrun.Result{}, err
		}
		if err := os.WriteFile(filepath.Join(repo, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			return gitrun.Result{}, err
		}
		return gitrun.Result{}, nil
	case "config":
		return gitrun.Result{}, nil
	case "fetch":
		s.mu.Lock()
		s.fetches++
		fail := s.fetches <= s.fetchFail
		s.mu.Unlock()
		if fail {
			line := s.fetchLine
			if line == "" {
				line = "fatal: bad object refs/member/other/HEAD"
			}
			return gitrun.Result{Stderr: []byte(line + "\n")}, errors.New("exit status 128")
		}
		return gitrun.Result{}, nil
	case "rev-parse":
		return gitrun.Result{Stdout: []byte(s.head + "\n")}, nil
	case "for-each-ref":
		if slices.Contains(args, "--contains") {
			if s.contains == "" {
				return gitrun.Result{}, nil
			}
			return gitrun.Result{Stdout: []byte(s.contains + "\n")}, nil
		}
		return gitrun.Result{}, nil
	case "merge-base":
		if s.notDescendant {
			return gitrun.Result{Stderr: []byte("fatal: not a descendant\n")}, errors.New("exit status 1")
		}
		return gitrun.Result{}, nil
	case "rev-list":
		return gitrun.Result{Stdout: []byte(s.commits + "\n")}, nil
	case "push":
		s.mu.Lock()
		s.pushes++
		fail := s.pushes <= s.pushFail
		s.mu.Unlock()
		if fail {
			return gitrun.Result{Stdout: []byte(s.pushLine + "\n")}, errors.New("exit status 1")
		}
		return gitrun.Result{}, nil
	case "update-ref":
		return gitrun.Result{}, nil
	}
	return gitrun.Result{}, fmt.Errorf("unscripted git %v", args)
}

// pushHarness is one member root under t.TempDir with the real files Push reads: the
// checkout <slots>/<launch>/jobs/c1/repo/.git, the slot's staged file, and the frame
// <slots>/<launch>.frame.json. Its git is scripted, its sleep records.
type pushHarness struct {
	t        *testing.T
	root     string
	slots    string
	launch   string
	checkout string
	origin   string
	p        member.Packet
	git      *scriptedGit
	waits    []time.Duration
}

func newPushHarness(t *testing.T) *pushHarness { return newPushHarnessAt(t, true) }

// newPushHarnessAt is newPushHarness with the checkout laid down or not: the card that
// names a repository but has no checkout is refused before any fetch.
func newPushHarnessAt(t *testing.T, checkout bool) *pushHarness {
	t.Helper()
	root := t.TempDir()
	h := &pushHarness{t: t, root: root, slots: filepath.Join(root, "slots"), origin: "/origin.git"}
	h.p = member.Packet{Card: "c1", Kind: "work", As: "m1", Primary: "p1", Stream: "s1",
		Attempt: 1, Gen: 1, Epoch: 7, Branch: "sprint/c1",
		Brief: "RESULT: c1\nbase-repo: " + h.origin + "\nBASE: main\n\nDo the work."}
	h.launch = launchName(h.p)
	h.checkout = filepath.Join(h.slots, h.launch, "jobs", "c1", swarm.JobRepo)
	if checkout {
		require.NoError(t, os.MkdirAll(filepath.Join(h.checkout, ".git"), 0o755))
	}
	write(t, filepath.Join(h.slots, h.launch, cardcontract.StagedName), strings.Repeat("b", 40)+"\n")
	require.NoError(t, cardcontract.WriteFrame(filepath.Join(h.slots, h.launch+cardcontract.FrameName),
		cardcontract.Frame{Kind: "work", Card: "c1", Repo: h.origin, BaseRef: "main"}))
	h.git = &scriptedGit{
		head:     strings.Repeat("a", 40),
		contains: "refs/member/" + h.launch + "/heads/rowan/c1",
		commits:  "1",
	}
	return h
}

// pusher is the member's pusher with the scripted seams: the scripted git, a sleep that
// records, and a gh that is on no PATH.
func (h *pushHarness) pusher() *gitPusher {
	g := newGitPusher(h.root, h.slots)
	g.git = h.git.run
	g.sleep = func(d time.Duration) { h.waits = append(h.waits, d) }
	g.gh = "nova-no-such-gh-for-memberpush-behaviour"
	return g
}

// A head that is not a sha is refused and git is never called (memberpush.go:88).
func TestAHeadThatIsNotAShaIsRefusedAndGitIsNeverCalled(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	got := h.pusher().Push(h.p, member.Result{Head: "not-a-sha"})
	assert.Contains(t, got.Refused, `the result's head "not-a-sha" is not a sha`)
	assert.Empty(t, got.Sha)
	assert.Empty(t, h.git.calls, "no git runs for a head that is not a sha")
}

// A branch that is not a branch name is refused and git is never called
// (memberpush.go:83-95; docs/SPEC-CARD-CONTRACT.md section 4).
func TestABranchThatIsNotABranchNameIsRefusedAndGitIsNeverCalled(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{"a..b", "x/", "x.lock", "+main", "a:b", "a b"} {
		t.Run(branch, func(t *testing.T) {
			t.Parallel()
			h := newPushHarness(t)
			p := h.p
			p.Branch = branch
			got := h.pusher().Push(p, member.Result{Head: h.git.head})
			assert.Contains(t, got.Refused, "is not a branch name", "branch %q", branch)
			assert.Empty(t, got.Sha)
			assert.Empty(t, h.git.calls, "no git runs for a branch that is not a branch name")
		})
	}
}

// A card that names no repository in the frame or the brief is not pushed: None
// (memberpush.go:96-102; docs/SPEC-CARD-CONTRACT.md layer 1).
func TestACardThatNamesNoRepositoryIsNotPushed(t *testing.T) {
	t.Parallel()
	h := newPushHarnessAt(t, true)
	require.NoError(t, os.Remove(filepath.Join(h.slots, h.launch+cardcontract.FrameName)))
	h.p.Brief = "RESULT: c1\nBASE: main\n\nDo the work."
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Equal(t, member.Push{None: "the card names no repository"}, got)
	assert.Empty(t, h.git.calls, "no git runs for a card that names no repository")
}

// A card that names a repository but has no checkout .git is refused
// (memberpush.go:103-106; docs/SPEC-CARD-CONTRACT.md layer 1).
func TestACardWithNoCheckoutIsRefused(t *testing.T) {
	t.Parallel()
	h := newPushHarnessAt(t, false)
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.Refused, "no checkout at "+h.checkout)
	assert.Empty(t, got.Sha)
	assert.Empty(t, got.None)
}

// The first use inits the member's push repository bare and sets its own configuration
// (memberpush.go:311-331; the gitPusher type comment).
func TestTheFirstUseInitsThePushRepositoryBareAndQuiet(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	require.Equal(t, member.Push{Sha: h.git.head}, h.pusher().Push(h.p, member.Result{Head: h.git.head}))
	pushRepo := filepath.Join(h.root, "push.git")
	var init, configs [][]string
	for _, a := range h.git.argv {
		switch a[0] {
		case "init":
			init = append(init, a)
		case "config":
			configs = append(configs, a)
		}
	}
	require.Len(t, init, 1, "push.git is inited once")
	assert.Equal(t, []string{"init", "-q", "--bare", "--", pushRepo}, init[0])
	assert.ElementsMatch(t, [][]string{
		{"config", "--", "gc.auto", "0"},
		{"config", "--", "maintenance.auto", "false"},
		{"config", "--", "core.hooksPath", os.DevNull},
	}, configs)
}

// An init that fails refuses the push naming git's own line (memberpush.go:319-322).
func TestAnInitThatFailsIsRefusedNamingGitsLine(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.initErr = true
	h.git.initLine = "fatal: could not create directory"
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.Refused, "the member's push repository: git init: fatal: could not create directory")
	assert.False(t, slices.ContainsFunc(h.git.argv, func(a []string) bool { return a[0] == "fetch" }),
		"a failed init stops before any fetch")
}

// A fetch from the checkout that fails every time is refused after the three waits
// (memberpush.go:120-136; pushWaits, memberpush.go:77).
func TestAFetchThatFailsEveryTimeIsRefusedAfterTheWaits(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.fetchFail = 4
	h.git.fetchLine = "fatal: bad object refs/member/c9.w1.g1.e7/HEAD"
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.Refused, "fetch from the checkout into the member's push repository, 4 tries: fatal: bad object refs/member/c9.w1.g1.e7/HEAD")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, h.waits)
	assert.Empty(t, got.Sha)
}

// A fetch that fails once and then answers lets the push go on, after one wait
// (memberpush.go:120-136).
func TestAFetchThatFailsOnceThenAnswersLetsThePushGoOn(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.fetchFail = 1
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Equal(t, member.Push{Sha: h.git.head}, got)
	assert.Equal(t, []time.Duration{time.Second}, h.waits)
}

// A head the checkout's branches or HEAD do not hold is refused
// (memberpush.go:143-147; docs/SPEC-CARD-CONTRACT.md section 4).
func TestAHeadTheCheckoutDoesNotHoldIsRefused(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.contains = ""
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Equal(t, member.Push{Refused: "the result's head " + h.git.head + " is not on this checkout's branches or HEAD"}, got)
	assert.Empty(t, got.Sha)
}

// A launch with no staged commit recorded is not pushed: None
// (memberpush.go:167-169; docs/SPEC-CARD-CONTRACT.md section 4).
func TestALaunchWithNoStagedCommitIsNotPushed(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.slots, h.launch, cardcontract.StagedName)))
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Equal(t, member.Push{None: "no staged commit is recorded for this launch to count the child's commits from"}, got)
}

// A head that does not descend from the staged commit is refused
// (memberpush.go:170-172; docs/SPEC-CARD-CONTRACT.md section 4; tla/CardContract.tla, Push).
func TestAHeadThatDoesNotDescendIsRefused(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.notDescendant = true
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.Refused, "does not descend from the staged commit")
	assert.Empty(t, got.Sha)
	assert.Empty(t, got.None)
}

// A head that is the staged commit or behind it is not pushed: the child committed
// nothing (memberpush.go:173-179; docs/SPEC-CARD-CONTRACT.md section 4).
func TestAHeadThatCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.commits = "0"
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, got.Sha)
	assert.Empty(t, got.Refused)
}

// A push origin rejected on its own side is sent again after 1 s, 2 s and 4 s, then
// refused with git's line (memberpush.go:185-195; pushWaits, memberpush.go:72-77).
func TestAPushTheRemoteRejectedIsSentAgainAfterTheWaits(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.pushFail = 4
	h.git.pushLine = "!\t0123:refs/heads/sprint/c1\t[remote rejected] (failed)"
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.Refused, "[remote rejected]")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, h.waits)
	assert.Empty(t, got.Sha)
}

// A push origin rejects for the commit (`[rejected]`, not a fast forward) is refused
// at once, with no wait and no force (memberpush.go:185-195; pushWaits, memberpush.go:72-77).
func TestAPushRejectedAsNotFastForwardIsRefusedAtOnceWithNoWait(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	h.git.pushFail = 1
	h.git.pushLine = "!\t0123:refs/heads/sprint/c1\t[rejected] (non-fast-forward)"
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	assert.Contains(t, got.Refused, "[rejected]")
	assert.NotContains(t, got.Refused, "[remote rejected]")
	assert.Empty(t, h.waits, "a rejection of the commit itself is never retried")
}

// Success: the full sha is pushed unforced from the member's own push repository, behind
// the separator, with no terminal prompt, and no git runs in the checkout
// (memberpush.go:180-195; the gitPusher type comment; docs/SPEC-SWARM.md, member).
func TestThePushIsUnforcedFromThePushRepositoryWithNoTerminalPrompt(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
	require.Equal(t, member.Push{Sha: h.git.head}, got)
	pushRepo := filepath.Join(h.root, "push.git")
	var push []string
	for i, a := range h.git.argv {
		assert.NotEqual(t, h.checkout, h.git.calls[i].C, "git %v ran in the checkout", a)
		assert.Contains(t, []string{"", pushRepo}, h.git.calls[i].C, "git %v ran somewhere else", a)
		if a[0] == "push" {
			push = a
			assert.Equal(t, pushRepo, h.git.calls[i].C)
			assert.Contains(t, h.git.calls[i].Env, "GIT_TERMINAL_PROMPT=0")
		}
	}
	require.NotNil(t, push, "a push was run")
	assert.Equal(t, []string{"push", "-q", "--porcelain", "--no-verify", "--", h.origin, h.git.head + ":refs/heads/sprint/c1"}, push)
	for _, a := range push {
		assert.False(t, strings.HasPrefix(a, "+"), "a forced refspec: %s", a)
	}
}

// A packet with a pull request title and no gh on PATH still pushes, and the Push's
// PRNote says why no pull request was opened (memberpush.go:200-204,260-289).
func TestAPullRequestWithNoGHOnPathIsNotedAndThePushStands(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	got := h.pusher().Push(h.p, member.Result{Head: h.git.head, Title: "a title", Body: "a body"})
	assert.Equal(t, h.git.head, got.Sha)
	assert.Empty(t, got.PR)
	assert.Contains(t, got.PRNote, "no nova-no-such-gh-for-memberpush-behaviour on this machine's PATH:")
	assert.NotContains(t, got.PRNote, "\n", "the note is one line")
}

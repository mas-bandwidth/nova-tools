package main

import (
	"context"
	"errors"
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

// This file pins the member's push of a child's commit, (g *gitPusher) Push
// (memberpush.go:88), in the unit tier with a scripted git: the type comment at
// memberpush.go:26-51 and docs/SPEC-SWARM.md's `member` are the contract. Every
// test builds the pusher with newGitPusher under t.TempDir, hands it a fake git
// that answers by argv and records each call's directory and environment, a sleep
// that records the waits (never sleeps), and a gh name that is not on PATH. No
// real git, no child, no socket, no clock. The fake cannot build the
// *exec.ExitError the not-a-descendant branch of checkoutTip reads; that branch
// is left to the slow tier (slow_test.go).

// mpbFullSha is the full sha a scripted rev-parse answers with, the child's head.
const mpbFullSha = "1111111111111111111111111111111111111111"

// mpbBase is the staged commit the slot records, a full sha the head must descend from.
const mpbBase = "2222222222222222222222222222222222222222"

// mpbErr is the error a failing scripted git returns beside its line.
var mpbErr = errors.New("scripted git refused")

// mpbCall is one git the scripted git was asked for: the directory it ran in ("" is
// the process's own), the environment it was handed, and its argv.
type mpbCall struct {
	C    string
	Env  []string
	Args []string
}

// mpbGit answers a git by its argv and records every call with its directory and
// environment. It is scripted through its fields; every failure carries git's own line
// on stderr (a push's porcelain `!` line on stdout, as the real git prints it).
type mpbGit struct {
	mu    sync.Mutex
	calls []mpbCall

	sha       string // rev-parse --verify's stdout
	count     string // rev-list --count's stdout
	objects   string // for-each-ref --format=%(objectname)'s stdout
	offBranch bool   // for-each-ref --contains answers nothing: the head is not this checkout's

	initLine   string // a fatal line an init fails with
	configLine string // a fatal line a config fails with
	fetchFail  int    // the first n fetches fail
	fetchLine  string // the line a failed fetch carries
	fetches    int
	pushFail   int    // the first n pushes fail
	pushLine   string // the porcelain line a failed push carries
	pushes     int

	mergeBaseErr string // a line the merge-base --is-ancestor call fails with
}

func (s *mpbGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, mpbCall{C: o.C, Env: slices.Clone(o.Env), Args: slices.Clone(args)})
	s.mu.Unlock()
	fail := func(line string) (gitrun.Result, error) {
		return gitrun.Result{Stderr: []byte(line + "\n")}, mpbErr
	}
	switch {
	case len(args) > 0 && args[0] == "init":
		if s.initLine != "" {
			return fail(s.initLine)
		}
		return gitrun.Result{}, nil
	case len(args) > 0 && args[0] == "config":
		if s.configLine != "" {
			return fail(s.configLine)
		}
		return gitrun.Result{}, nil
	case len(args) > 0 && args[0] == "fetch":
		s.mu.Lock()
		s.fetches++
		n := s.fetches
		s.mu.Unlock()
		if n <= s.fetchFail {
			return fail(s.fetchLine)
		}
		return gitrun.Result{}, nil
	case len(args) > 0 && args[0] == "rev-parse":
		return gitrun.Result{Stdout: []byte(s.sha + "\n")}, nil
	case len(args) > 0 && args[0] == "for-each-ref":
		if slices.Contains(args, "--contains") {
			if s.offBranch {
				return gitrun.Result{}, nil
			}
			return gitrun.Result{Stdout: []byte("refs/member/x/heads/b\n")}, nil
		}
		for _, a := range args {
			if strings.HasPrefix(a, "--format=delete") {
				return gitrun.Result{}, nil
			}
		}
		// checkoutTip's --format=%(objectname): the candidate tails of the checkout
		return gitrun.Result{Stdout: []byte(s.objects + "\n")}, nil
	case len(args) > 0 && args[0] == "merge-base":
		if s.mergeBaseErr != "" {
			return fail(s.mergeBaseErr)
		}
		return gitrun.Result{}, nil
	case len(args) > 0 && args[0] == "rev-list":
		return gitrun.Result{Stdout: []byte(s.count + "\n")}, nil
	case len(args) > 0 && args[0] == "push":
		s.mu.Lock()
		s.pushes++
		n := s.pushes
		s.mu.Unlock()
		if n <= s.pushFail {
			return gitrun.Result{Stdout: []byte(s.pushLine + "\n")}, mpbErr
		}
		return gitrun.Result{}, nil
	}
	return gitrun.Result{}, nil
}

// mpbSnapshot is the recorded calls, copied under the lock so a test may read them.
func (s *mpbGit) mpbSnapshot() []mpbCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// mpbArgvOf is the argv of each recorded call whose first word is sub.
func (s *mpbGit) mpbArgvOf(sub string) [][]string {
	var out [][]string
	for _, c := range s.mpbSnapshot() {
		if len(c.Args) > 0 && c.Args[0] == sub {
			out = append(out, c.Args)
		}
	}
	return out
}

// mpbConfig lays out the real files a push reads, or omits them for a refusal.
type mpbConfig struct {
	noRepo     bool // the frame and the brief name no repository
	noFrame    bool // no frame: only the brief's base-repo: line names the repository
	noCheckout bool // the checkout's .git is absent
	noStaged   bool // the slot records no staged commit
}

// mpbRig is one member root, a slot's launch, the checkout native staged, and the
// pusher over a scripted git.
type mpbRig struct {
	t        *testing.T
	root     string
	slots    string
	launch   string
	checkout string
	p        member.Packet
	head     string
	git      *mpbGit
	waits    []time.Duration
	g        *gitPusher
}

// newMPBRig lays out the frame (<slots>/<launch>.frame.json), the checkout's .git, and
// the slot's staged file, and builds the pusher over a scripted git and a recorded sleep.
func newMPBRig(t *testing.T, cfg mpbConfig) *mpbRig {
	t.Helper()
	root := t.TempDir()
	slots := filepath.Join(root, "slots")
	origin := filepath.Join(root, "origin.git")
	p := member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 7, Branch: "sprint/c1",
		Brief: "RESULT: c1\nbase-repo: " + origin + "\n"}
	if cfg.noRepo {
		p.Brief = "RESULT: c1\n"
	}
	launch := launchName(p)
	checkout := filepath.Join(slots, launch, "jobs", p.Card, swarm.JobRepo)
	require.NoError(t, os.MkdirAll(filepath.Dir(checkout), 0o755))
	if !cfg.noFrame {
		repo := origin
		if cfg.noRepo {
			repo = ""
		}
		require.NoError(t, cardcontract.WriteFrame(filepath.Join(slots, launch+cardcontract.FrameName),
			cardcontract.Frame{Kind: "work", Card: p.Card, Repo: repo, BaseRef: "main"}))
	}
	if !cfg.noCheckout {
		require.NoError(t, os.MkdirAll(filepath.Join(checkout, ".git"), 0o755))
	}
	if !cfg.noStaged {
		require.NoError(t, os.WriteFile(filepath.Join(slots, launch, cardcontract.StagedName), []byte(mpbBase+"\n"), 0o644))
	}
	s := &mpbGit{sha: mpbFullSha, count: "2"}
	r := &mpbRig{t: t, root: root, slots: slots, launch: launch, checkout: checkout, p: p, head: mpbFullSha, git: s}
	g := newGitPusher(root, slots)
	g.git = s.run
	g.sleep = func(d time.Duration) { r.waits = append(r.waits, d) }
	g.gh = "nova-no-such-gh-for-mpb"
	r.g = g
	return r
}

// result is the child's result naming the head, as Push is asked.
func (r *mpbRig) result() member.Result { return member.Result{Head: r.head} }

// TestAPushRefusesAHeadThatIsNotASha pins "the result's head %q is not a sha"
// (memberpush.go:91) before any git runs.
func TestAPushRefusesAHeadThatIsNotASha(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, head string }{
		{"a word", "not-a-sha"},
		{"an empty head", ""},
		{"no head at all", "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMPBRig(t, mpbConfig{})
			got := r.g.Push(r.p, member.Result{Head: tc.head})
			assert.Empty(t, got.Sha)
			assert.Empty(t, got.None)
			assert.Contains(t, got.Refused, "is not a sha")
			assert.Empty(t, r.git.mpbSnapshot(), "a head that is not a sha calls no git")
		})
	}
}

// TestAPushRefusesABranchThatIsNotABranchName pins memberpush.go:93 and pushBranchRE
// (memberpush.go:85): a refspec character or a blank is never a branch name.
func TestAPushRefusesABranchThatIsNotABranchName(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{"a..b", "x/", "x.lock", "+main", "a:b", "a b"} {
		t.Run(branch, func(t *testing.T) {
			t.Parallel()
			r := newMPBRig(t, mpbConfig{})
			r.p.Branch = branch
			got := r.g.Push(r.p, r.result())
			assert.Empty(t, got.Sha, "branch %q", branch)
			assert.Contains(t, got.Refused, `the packet's branch "`+branch+`" is not a branch name`)
			assert.Empty(t, r.git.mpbSnapshot(), "a branch that is not a branch name calls no git")
		})
	}
}

// TestAPushRefusesACardWithNoRepositoryOrNoCheckout pins memberpush.go:101 (None
// "the card names no repository") and memberpush.go:105 (Refused "no checkout at ...").
func TestAPushRefusesACardWithNoRepositoryOrNoCheckout(t *testing.T) {
	t.Parallel()
	t.Run("no repository in the frame or the brief", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{noRepo: true})
		got := r.g.Push(r.p, r.result())
		assert.Equal(t, member.Push{None: "the card names no repository"}, got)
		assert.Empty(t, r.git.mpbSnapshot(), "a card with no repository calls no git")
	})
	t.Run("a named repository with no checkout", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{noCheckout: true})
		got := r.g.Push(r.p, r.result())
		assert.Equal(t, "", got.Sha)
		assert.Contains(t, got.Refused, "no checkout at "+filepath.Join(r.slots, r.launch, "jobs", "c1", swarm.JobRepo)+" to push "+r.head+" from")
	})
}

// TestAPushInitialisesThePushRepositoryOnFirstUse pins repo (memberpush.go:313): a
// first use is init --bare of <root>/push.git and its config; a failing init is
// Refused naming git's line (memberpush.go:321).
func TestAPushInitialisesThePushRepositoryOnFirstUse(t *testing.T) {
	t.Parallel()
	t.Run("first use inits the bare repo and configures it", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		require.Equal(t, member.Push{Sha: r.head}, r.g.Push(r.p, r.result()))
		calls := r.git.mpbSnapshot()
		repo := filepath.Join(r.root, "push.git")
		require.NotEmpty(t, calls)
		assert.Equal(t, []string{"init", "-q", "--bare", "--", repo}, calls[0].Args)
		assert.Empty(t, calls[0].C, "init runs with no directory")
		want := [][]string{
			{"config", "--", "gc.auto", "0"},
			{"config", "--", "maintenance.auto", "false"},
			{"config", "--", "core.hooksPath", os.DevNull},
		}
		require.GreaterOrEqual(t, len(calls), 4)
		for i, w := range want {
			assert.Equal(t, w, calls[i+1].Args)
			assert.Equal(t, repo, calls[i+1].C, "config runs in the push repository")
		}
	})
	t.Run("a failing init refuses naming git's line", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.initLine = "fatal: could not create directory 'push.git'"
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Contains(t, got.Refused, "the member's push repository: git init: fatal: could not create directory 'push.git'")
	})
}

// TestAPushFetchesFromTheCheckoutAgainWhenItFails pins memberpush.go:120-136 and
// pushWaits (memberpush.go:77): a fetch that fails is made again after 1s, 2s and 4s,
// and a fetch that fails every time is the refusal; one that fails once lands.
func TestAPushFetchesFromTheCheckoutAgainWhenItFails(t *testing.T) {
	t.Parallel()
	t.Run("a fetch that fails every time is refused after the waits", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.fetchFail = len(pushWaits) + 1
		r.git.fetchLine = "fatal: bad object refs/member/c9/HEAD"
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Equal(t, "fetch from the checkout into the member's push repository, 4 tries: fatal: bad object refs/member/c9/HEAD", got.Refused)
		assert.Equal(t, pushWaits, r.waits)
		assert.Len(t, r.git.mpbArgvOf("fetch"), 4)
	})
	t.Run("a fetch that fails once then answers lands the head", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.fetchFail = 1
		r.git.fetchLine = "fatal: bad object refs/member/c9/HEAD"
		require.Equal(t, member.Push{Sha: r.head}, r.g.Push(r.p, r.result()))
		assert.Equal(t, pushWaits[:1], r.waits)
		assert.Len(t, r.git.mpbArgvOf("fetch"), 2)
	})
}

// TestAPushJudgesTheHeadAgainstTheStagedCommit pins memberpush.go:143 (Refused "not
// on this checkout's branches or HEAD"), memberpush.go:167 (None "no staged commit"),
// memberpush.go:170 (Refused "does not descend") and memberpush.go:177 (None "the
// child committed nothing"), the counts from cardcontract.StagedName (SPEC-CARD-CONTRACT.md 4).
func TestAPushJudgesTheHeadAgainstTheStagedCommit(t *testing.T) {
	t.Parallel()
	t.Run("a head not on the checkout's branches or HEAD is refused", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.offBranch = true
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Equal(t, "the result's head "+r.head+" is not on this checkout's branches or HEAD", got.Refused)
	})
	t.Run("no staged commit recorded is none", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{noStaged: true})
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Equal(t, "no staged commit is recorded for this launch to count the child's commits from", got.None)
	})
	t.Run("a head that does not descend from the staged commit is refused", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.mergeBaseErr = "fatal: Not a valid commit name " + mpbBase
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Equal(t, "the result's head "+r.head+" does not descend from the staged commit "+mpbBase+": fatal: Not a valid commit name "+mpbBase, got.Refused)
	})
	t.Run("zero commits past the staged commit is none", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.count = "0"
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Equal(t, "the child committed nothing: head "+r.head+" is the staged commit or behind it", got.None)
	})
}

// TestAPushResendsARemoteRejectedPush pins memberpush.go:185-195 and pushWaits
// (memberpush.go:72-77): a push origin rejected on its own side is sent again after
// 1s, 2s and 4s; one refused for the commit itself is refused at once.
func TestAPushResendsARemoteRejectedPush(t *testing.T) {
	t.Parallel()
	t.Run("a remote rejection every time is refused after the waits", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.pushFail = len(pushWaits) + 1
		r.git.pushLine = "!\t0123:refs/heads/sprint/c1\t[remote rejected] (failed)"
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Contains(t, got.Refused, "[remote rejected]")
		assert.Len(t, r.git.mpbArgvOf("push"), 4)
		assert.Equal(t, pushWaits, r.waits)
	})
	t.Run("a push refused for the commit is refused at once", func(t *testing.T) {
		t.Parallel()
		r := newMPBRig(t, mpbConfig{})
		r.git.pushFail = 1
		r.git.pushLine = "!\t0123:refs/heads/sprint/c1\t[rejected] (non-fast-forward)"
		got := r.g.Push(r.p, r.result())
		assert.Empty(t, got.Sha)
		assert.Contains(t, got.Refused, "[rejected]")
		assert.Empty(t, r.waits, "a push refused for the commit is never sent again")
		assert.Len(t, r.git.mpbArgvOf("push"), 1)
	})
}

// TestAPushLandsTheHeadWithoutForceAndWithATerminalPromptRefused pins the push's
// shape (memberpush.go:184) and run's environment (memberpush.go:355): the head is
// pushed from the member's own push.git, never forced, with GIT_TERMINAL_PROMPT=0.
func TestAPushLandsTheHeadWithoutForceAndWithATerminalPromptRefused(t *testing.T) {
	t.Parallel()
	r := newMPBRig(t, mpbConfig{noFrame: true}) // the brief's base-repo: line names the repo
	got := r.g.Push(r.p, r.result())
	require.Equal(t, r.head, got.Sha)
	require.Equal(t, member.Push{Sha: r.head}, got)
	repo := filepath.Join(r.root, "push.git")
	pushes := r.git.mpbArgvOf("push")
	require.Len(t, pushes, 1)
	assert.Equal(t, []string{"push", "-q", "--porcelain", "--no-verify", "--",
		filepath.Join(r.root, "origin.git"), r.head + ":refs/heads/sprint/c1"}, pushes[0])
	for _, c := range r.git.mpbSnapshot() {
		assert.Contains(t, c.Env, "GIT_TERMINAL_PROMPT=0")
		assert.True(t, c.C == "" || c.C == repo, "every git runs in push.git or with no directory, got %q", c.C)
		assert.NotEqual(t, r.checkout, c.C, "no git runs in the checkout")
	}
}

// TestAPushWithAPullRequestTitleAndNoGhStillLands pins openPR's missing-gh note
// (memberpush.go:267): a pull request asked for with no gh on PATH says so, and the
// push still lands (memberpush.go:200-204).
func TestAPushWithAPullRequestTitleAndNoGhStillLands(t *testing.T) {
	t.Parallel()
	r := newMPBRig(t, mpbConfig{})
	res := member.Result{Head: r.head, Title: "a pull request title", Body: "the body"}
	got := r.g.Push(r.p, res)
	require.Equal(t, r.head, got.Sha, "the push still succeeds")
	assert.Empty(t, got.PR)
	assert.True(t, strings.HasPrefix(got.PRNote, "no nova-no-such-gh-for-mpb on this machine's PATH: "),
		"the note names the missing gh, got %q", got.PRNote)
	assert.NotContains(t, got.PRNote, "\n", "the note is one line")
}

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// pushBench is one member root with an origin (a bare repository with main
// at one commit), a work card c1 whose brief names that origin, and the
// card's checkout where native stages it: <slots>/<launch>/jobs/c1/repo, a
// clone of origin on the branch staging names.
type pushBench struct {
	root, slots, origin, checkout, base string
	p                                   member.Packet
}

// gitAs is a git with an identity, for the commits a test makes.
func gitAs(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...))
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

// The member pushes the child's commit to origin's branch the sprint named,
// from its own repository: the checkout's pre-push hook, credential helper and
// ssh command (the child's, written inside the wall) never run, and the
// launch's refs are gone from the push repository after.
func TestThePushPutsTheChildsCommitOnOriginsBranch(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	marks := filepath.Join(b.root, "marks")
	hook := filepath.Join(b.checkout, ".git", "hooks", "pre-push")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marks+"-hook\n"), 0o755))
	runGit(t, b.checkout, "config", "credential.helper", "!touch "+marks+"-credential")
	runGit(t, b.checkout, "config", "core.sshCommand", "touch "+marks+"-ssh")
	runGit(t, b.checkout, "config", "core.fsmonitor", "touch "+marks+"-fsmonitor")
	runGit(t, b.checkout, "config", "remote.origin.pushurl", filepath.Join(b.root, "elsewhere.git"))

	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: head[:12]})

	assert.Equal(t, member.Push{Sha: head}, got)
	assert.Equal(t, head, b.originHas(t, "sprint/c1"), "origin's branch holds the child's commit")
	for _, m := range []string{"-hook", "-credential", "-ssh", "-fsmonitor"} {
		assert.NoFileExists(t, marks+m, "the checkout's own configuration ran with the member's credential")
	}
	assert.NoDirExists(t, filepath.Join(b.root, "elsewhere.git"), "the checkout's pushurl is never read")
	assert.Empty(t, runGit(t, filepath.Join(b.root, "push.git"), "for-each-ref", "refs/member/"), "the launch's refs are dropped after the push")
}

// A second push of the same card is the same commit: origin is up to date and
// the push says so as a push, never a refusal.
func TestAPushRepeatedIsTheSamePush(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
}

// A child that committed nothing (its head is origin's) is not pushed, and
// origin has no branch for it.
func TestAChildThatCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: b.base})
	assert.Empty(t, got.Sha)
	assert.Empty(t, got.Refused)
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// A branch origin holds at another commit refuses the push with git's own
// line, and is left as it was: the member never forces.
func TestAPushOriginRefusesIsRefusedWithGitsLineAndNeverForced(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	other := filepath.Join(b.root, "other")
	runGit(t, "", "clone", "-q", "--", b.origin, other)
	require.NoError(t, os.WriteFile(filepath.Join(other, "f"), []byte("someone else\n"), 0o644))
	gitAs(t, other, "commit", "-q", "-am", "someone else")
	theirs := gitAs(t, other, "rev-parse", "HEAD")
	runGit(t, other, "push", "-q", "origin", "HEAD:refs/heads/sprint/c1")
	head := b.commit(t, "the work\n")

	got := b.pusher().Push(b.p, member.Result{Head: head})

	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "[rejected]")
	assert.NotContains(t, got.Refused, "\t", "git's line is one line, no tab")
	assert.Equal(t, theirs, b.originHas(t, "sprint/c1"), "the branch is never forced")
}

// recordGit is a git that records every argv and directory it is asked for
// and runs it, or refuses a push with the line given.
type recordGit struct {
	mu         sync.Mutex
	calls      []gitrun.Options
	argv       [][]string
	refusePush string
}

func (r *recordGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	r.mu.Lock()
	r.calls, r.argv = append(r.calls, o), append(r.argv, slices.Clone(args))
	r.mu.Unlock()
	if r.refusePush != "" && slices.Contains(args, "push") {
		return gitrun.Result{Stderr: []byte("To the origin\n" + r.refusePush + "\n")}, assert.AnError
	}
	return gitrun.Run(ctx, o, args...)
}

// The push's own argv: from the member's push repository (never the
// checkout), --no-verify, `--` before the URL and the refspec, the full sha
// onto the sprint's branch, and no force in any form.
func TestThePushArgvIsUnforcedAndBehindTheSeparator(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	rec := &recordGit{}
	g := b.pusher()
	g.git = rec.run
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	var push []string
	for i, a := range rec.argv {
		assert.NotEqual(t, b.checkout, rec.calls[i].C, "git %v ran in the checkout", a)
		if len(a) > 0 && a[0] == "push" {
			push = a
			assert.Equal(t, filepath.Join(b.root, "push.git"), rec.calls[i].C)
			assert.Contains(t, rec.calls[i].Env, "GIT_TERMINAL_PROMPT=0", "a push with no credential is refused, never left at a prompt")
		}
	}
	require.NotNil(t, push, "a push was run")
	assert.Equal(t, []string{"push", "-q", "--porcelain", "--no-verify", "--", b.origin, head + ":refs/heads/sprint/c1"}, push)
	for _, a := range push {
		assert.NotContains(t, []string{"-f", "--force", "--force-with-lease", "--mirror", "--delete"}, a)
		assert.False(t, strings.HasPrefix(a, "+"), "a forced refspec: %s", a)
	}
}

// A git that refuses the push (a machine with no credential for origin) is a
// refusal carrying git's own fatal line.
func TestAGitThatRefusesThePushIsRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	line := "fatal: could not read Username for the origin: terminal prompts disabled"
	g := b.pusher()
	g.git = (&recordGit{refusePush: line}).run
	assert.Equal(t, member.Push{Refused: line}, g.Push(b.p, member.Result{Head: head}))
	assert.Empty(t, b.originHas(t, "sprint/c1"))
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

// A push origin rejected on its own side is the remote's failure, not the
// commit's: it is sent again after a wait, and the commit lands. Origin rejecting
// it every time is the refusal, with git's line, after the last wait; a push
// refused for the commit itself is never sent again.
func TestAPushTheRemoteRejectedIsSentAgain(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	var waits []time.Duration
	g := b.pusher()
	twice := &rejectGit{reject: 2}
	g.git, g.sleep = twice.run, func(d time.Duration) { waits = append(waits, d) }
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, 3, twice.pushes, "rejected twice, landed on the third")
	assert.Equal(t, pushWaits[:2], waits)
	assert.Equal(t, head, b.originHas(t, "sprint/c1"))

	b2 := newPushBench(t)
	head2 := b2.commit(t, "the work\n")
	waits = nil
	g2 := b2.pusher()
	always := &rejectGit{reject: 100}
	g2.git, g2.sleep = always.run, func(d time.Duration) { waits = append(waits, d) }
	got := g2.Push(b2.p, member.Result{Head: head2})
	assert.Contains(t, got.Refused, "[remote rejected]")
	assert.Equal(t, len(pushWaits)+1, always.pushes)
	assert.Equal(t, pushWaits, waits)
	assert.Empty(t, b2.originHas(t, "sprint/c1"))

	b3 := newPushBench(t)
	head3 := b3.commit(t, "the work\n")
	waits = nil
	g3 := b3.pusher()
	rec := &recordGit{refusePush: "!\t0123:refs/heads/sprint/c1\t[rejected] (non-fast-forward)"}
	g3.git, g3.sleep = rec.run, func(d time.Duration) { waits = append(waits, d) }
	got = g3.Push(b3.p, member.Result{Head: head3})
	assert.Contains(t, got.Refused, "[rejected]")
	assert.Empty(t, waits, "a push refused for the commit is not sent again")
}

// badFetchGit is a git whose first n fetches into the push repository fail as one did on the
// 5000-card load test (2026-10-01), on another launch's ref, while the bench mirror the push
// repository borrows objects from was being repacked; every other git, and the fetches
// after, run. At each push it records the launch refs the push repository still holds.
type badFetchGit struct {
	mu          sync.Mutex
	fail        int
	fetches     int
	refsAtPush  []string
	refsAtFetch []string
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
		if failed {
			return gitrun.Result{Stderr: []byte(badFetchLine + "\n")}, assert.AnError
		}
	case slices.Contains(args, "push"):
		r.mu.Lock()
		r.refsAtPush = append(r.refsAtPush, held()...)
		r.mu.Unlock()
	}
	return gitrun.Run(ctx, o, args...)
}

// A fetch from the checkout that fails is the member's moment, never the card's: it is made
// again after a wait, with nothing of the failed try left in this launch's namespace, and the
// commit lands; a fetch that fails every time is the refusal, saying the push repository and
// the tries. The launch's refs are gone before the push to origin, so another launch's fetch
// never meets them for the seconds a push and a pull request take. On the 5000-card load test
// (2026-10-01) one card failed "push refused: fetch from the checkout: fatal: bad object
// refs/member/<another launch>/HEAD", a moment of the shared push repository.
func TestAFetchFromTheCheckoutThatFailsIsMadeAgain(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	var waits []time.Duration
	g := b.pusher()
	once := &badFetchGit{fail: 1}
	g.git, g.sleep = once.run, func(d time.Duration) { waits = append(waits, d) }
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, 2, once.fetches, "failed once, fetched on the second")
	assert.Equal(t, pushWaits[:1], waits)
	assert.Empty(t, once.refsAtFetch, "each try starts with this launch's namespace empty")
	assert.Empty(t, once.refsAtPush, "the launch's refs are dropped before the push to origin")
	assert.Equal(t, head, b.originHas(t, "sprint/c1"))

	b2 := newPushBench(t)
	head2 := b2.commit(t, "the work\n")
	waits = nil
	g2 := b2.pusher()
	always := &badFetchGit{fail: 100}
	g2.git, g2.sleep = always.run, func(d time.Duration) { waits = append(waits, d) }
	got := g2.Push(b2.p, member.Result{Head: head2})
	assert.Empty(t, got.Sha)
	assert.Equal(t, fmt.Sprintf("fetch from the checkout into the member's push repository, %d tries: %s", len(pushWaits)+1, badFetchLine), got.Refused)
	assert.Equal(t, len(pushWaits)+1, always.fetches)
	assert.Equal(t, pushWaits, waits)
	assert.Empty(t, b2.originHas(t, "sprint/c1"))
}

// What is not pushed, each said: a head that is not a sha, a branch that is
// not a branch name (a refspec in disguise), a card that names no repository,
// a checkout that is not there, a head the checkout's branches do not hold (with two lines
// of work on them, so the checkout has no one head of its own to push in its place).
func TestWhatIsNotPushedIsSaid(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	cases := []struct {
		name    string
		p       func(member.Packet) member.Packet
		head    string
		refused string
		none    string
	}{
		{"a head that is not a sha", nil, "--upload-pack=x", `the result's head "--upload-pack=x" is not a sha`, ""},
		{"a branch with a refspec colon", func(p member.Packet) member.Packet { p.Branch = "x:refs/heads/main"; return p }, head, "is not a branch name", ""},
		{"a branch that starts with a dash", func(p member.Packet) member.Packet { p.Branch = "-f"; return p }, head, "is not a branch name", ""},
		{"a branch with ..", func(p member.Packet) member.Packet { p.Branch = "sprint/../main"; return p }, head, "is not a branch name", ""},
		{"a card that names no repository", func(p member.Packet) member.Packet { p.Brief = "RESULT: c1\n\nDo it."; return p }, head, "", "the card names no repository"},
		{"no checkout", func(p member.Packet) member.Packet { p.Gen = 9; return p }, head, "no checkout at ", ""},
		{"a head the branches do not hold", nil, "deadbeefdeadbeef", "is not a commit on the checkout's branches", ""},
	}
	for _, tc := range cases {
		p := b.p
		if tc.p != nil {
			p = tc.p(p)
		}
		got := g.Push(p, member.Result{Head: tc.head})
		assert.Empty(t, got.Sha, tc.name)
		if tc.refused != "" {
			assert.Contains(t, got.Refused, tc.refused, tc.name)
		}
		if tc.none != "" {
			assert.Equal(t, tc.none, got.None, tc.name)
		}
	}
	assert.Empty(t, b.originHas(t, "sprint/c1"), "nothing of the refused was pushed")
	assert.Equal(t, b.base, b.originHas(t, "main"), "origin's main is as it was")
}

// A rework staged at the attempt before's pushed head, whose checkout's
// origin refs do not hold that head (a stale bench mirror), and whose child
// committed nothing, is not pushed: the child's commits are counted from the
// staged commit, never from the checkout's refs (docs/SPEC-CARD-CONTRACT.md
// section 4; red before the count moved off the refs).
func TestAReworkOnAStaleMirrorThatCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	h1 := b.commit(t, "attempt one\n") // on no origin ref the checkout knows: a stale mirror
	b.staged(t, h1)
	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: h1})
	assert.Empty(t, got.Sha, "attempt one's head is not attempt two's commit")
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// A child that removed the checkout's remote and committed nothing is not
// pushed either: its refs are not the member's evidence.
func TestAChildThatRemovedTheRemoteAndCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	runGit(t, b.checkout, "remote", "remove", "origin")
	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: b.base})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// A commit another launch left in the member's push repository is not this
// launch's: the head must be on this checkout's branches or HEAD, and descend
// from the commit this launch was staged at (docs/SPEC-CARD-CONTRACT.md
// section 4; red when the count alone decided).
func TestACommitAnotherLaunchLeftInThePushRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	g := b.pusher()
	foreign := b.commit(t, "card one's change\n")
	require.Equal(t, member.Push{Sha: foreign}, g.Push(b.p, member.Result{Head: foreign}), "card one's push leaves its objects in push.git")

	otherSeed, otherOrigin := filepath.Join(b.root, "other-seed"), filepath.Join(b.root, "other.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", otherSeed)
	require.NoError(t, os.WriteFile(filepath.Join(otherSeed, "f"), []byte("other base\n"), 0o644))
	gitAs(t, otherSeed, "add", "f")
	gitAs(t, otherSeed, "commit", "-q", "-m", "other base")
	runGit(t, "", "clone", "-q", "--bare", "--", otherSeed, otherOrigin)
	p2 := member.Packet{Card: "c2", Kind: "work", As: "m1", Primary: "p2", Stream: "s2", Attempt: 1, Gen: 1, Epoch: 7,
		Brief: "RESULT: c2\nbase-repo: " + otherOrigin + "\nBASE: main\n\nOther work.", Branch: "sprint/c2"}
	checkout := filepath.Join(b.slots, launchName(p2), "jobs", "c2", swarm.JobRepo)
	runGit(t, "", "clone", "-q", "--", otherOrigin, checkout)
	write(t, filepath.Join(b.slots, launchName(p2), "staged"), gitAs(t, otherOrigin, "rev-parse", "main")+"\n")

	got := g.Push(p2, member.Result{Head: foreign})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "is not on this checkout's branches or HEAD")
	assert.Empty(t, strings.TrimSpace(runGit(t, otherOrigin, "for-each-ref", "refs/heads/sprint/c2")))
}

// A head on this checkout that does not descend from the staged commit (the
// child reset to another history) is refused, never pushed.
func TestAHeadThatDoesNotDescendFromTheStagedCommitIsRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	gitAs(t, b.checkout, "checkout", "-q", "--orphan", "elsewhere")
	gitAs(t, b.checkout, "commit", "-q", "-m", "another history")
	orphan := gitAs(t, b.checkout, "rev-parse", "HEAD")
	b.staged(t, head)
	got := b.pusher().Push(b.p, member.Result{Head: orphan})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "does not descend from the staged commit")
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

// A result whose head names no commit of the checkout (its tail invented), from a child that
// made exactly one line of work, pushes the checkout's own head and says so: five of the first
// twelve failures of the load test were this, the commit on the checkout's branch all along.
func TestAWrongTailHeadPushesTheCheckoutsOwnCommit(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	claimed := wrongTail(head)
	g := b.pusher()
	var notes strings.Builder
	g.notes = &notes

	got := g.Push(b.p, member.Result{Ran: true, OK: true, Head: claimed})

	assert.Equal(t, member.Push{Sha: head}, got)
	assert.Equal(t, head, b.originHas(t, "sprint/c1"), "origin's branch holds the checkout's commit")
	assert.Equal(t, "NOTE push c1 head: the result named "+claimed+", which is no commit of the checkout; the checkout's own head "+head+" was pushed\n", notes.String())
}

// A result head the checkout does not hold, with two lines of work on its branches, is refused
// as before: the member does not choose between them. With no line of work it is refused too.
func TestAnAbsentHeadWithTwoCandidateBranchesIsStillRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	var notes strings.Builder
	g.notes = &notes
	got := g.Push(b.p, member.Result{Head: wrongTail(head)})
	assert.Equal(t, member.Push{Refused: "the result's head " + wrongTail(head) + " is not a commit on the checkout's branches"}, got)
	assert.Empty(t, b.originHas(t, "sprint/c1"))
	assert.Empty(t, notes.String())

	none := newPushBench(t)
	got = none.pusher().Push(none.p, member.Result{Head: wrongTail(none.base)})
	assert.Equal(t, member.Push{Refused: "the result's head " + wrongTail(none.base) + " is not a commit on the checkout's branches"}, got, "no line of work: nothing to push in its place")
	assert.Empty(t, none.originHas(t, "sprint/c1"))
}

// A result head the checkout holds is pushed exactly as before, and nothing is noted, even
// when the checkout holds a second line of work beside it.
func TestARightHeadIsPushedAsBefore(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	var notes strings.Builder
	g.notes = &notes
	assert.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, head, b.originHas(t, "sprint/c1"))
	assert.Empty(t, notes.String())
}

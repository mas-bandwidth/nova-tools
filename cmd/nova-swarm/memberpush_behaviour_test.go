package main

// The unit tier's push test: (g *gitPusher) Push (memberpush.go:88) pinned with a
// scripted git, so a push runs with no real git, no checkout and no clock. The
// contract is the type's comment (memberpush.go:26-51), docs/SPEC-SWARM.md (member)
// and docs/SPEC-CARD-CONTRACT.md section 4. Every helper here is this card's own,
// named apart from the slow tier's pushBench, recordGitRun, rejectGit, badFetchGit
// and wrongTail (slow_helpers_test.go), which share this package under -tags slow.
//
// A fake cannot build the *exec.ExitError the not-a-descendant branch of
// checkoutTip reads (memberpush.go:229); that branch is left to the slow tier.

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

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pushed head and the staged commit it counts its commits from: forty hex digits.
const (
	unitPushHead = "1111111111111111111111111111111111111111"
	unitPushBase = "2222222222222222222222222222222222222222"
	unitRepoURL  = "https://example.com/o/n.git"
	unitNoGH     = "nova-no-such-gh-for-scripted-push"
)

// unitPush is one member push stood up for the unit tier: a temp root, the
// launch's slot, the checkout the push reads (<slots>/<launch>/jobs/<card>/repo,
// with its .git directory), the launch's staged file, and a brief naming the
// card's repository. gitPusher.git and gitPusher.sleep are the seams; no real
// git runs and no wait is slept.
type unitPush struct {
	t        *testing.T
	root     string
	slots    string
	launch   string
	checkout string
	p        member.Packet
	git      *scriptedPushGit
	sleeps   []time.Duration
	pusher   *gitPusher
}

func newUnitPush(t *testing.T) *unitPush {
	t.Helper()
	u := newUnitPushBase(t)
	write(t, filepath.Join(u.checkout, ".git", "HEAD"), "ref: refs/heads/sprint/c1\n")
	return u
}

// newUnitPushBase is newUnitPush without the checkout laid out: the
// no-checkout refusal's subject.
func newUnitPushBase(t *testing.T) *unitPush {
	t.Helper()
	root := t.TempDir()
	u := &unitPush{t: t, root: root, slots: filepath.Join(root, "slots")}
	u.p = member.Packet{Card: "c1", Kind: "work", As: "m1", Primary: "p1", Stream: "s1",
		Attempt: 1, Gen: 1, Epoch: 7, Branch: "sprint/c1",
		Brief: "RESULT: c1\nbase-repo: " + unitRepoURL + "\nBASE: main\n\nDo the work.\n"}
	u.launch = launchName(u.p)
	u.checkout = filepath.Join(u.slots, u.launch, "jobs", u.p.Card, swarm.JobRepo)
	u.staged(unitPushBase)
	u.git = &scriptedPushGit{revParse: unitPushHead, branchHit: true, descend: true, commits: "1"}
	u.pusher = newGitPusher(root, u.slots)
	u.pusher.git = u.git.run
	u.pusher.sleep = func(d time.Duration) { u.sleeps = append(u.sleeps, d) }
	u.pusher.gh = unitNoGH
	return u
}

// staged records the commit native staged the launch at, in the slot, as native
// does (cardcontract.StagedName).
func (u *unitPush) staged(sha string) {
	write(u.t, filepath.Join(u.slots, u.launch, cardcontract.StagedName), sha+"\n")
}

// scriptedPushGit is the unit tier's git: it answers each argv gitPusher sends and
// records every call with its -C directory and environment. Its fields script the
// answers a test needs, so a Push exercises the pusher's own logic alone.
type scriptedPushGit struct {
	mu         sync.Mutex
	calls      []gitrun.Options
	argvs      [][]string
	initFail   bool
	initLine   string
	fetchFail  int
	fetchLine  string
	fetches    int
	pushFail   int
	pushStdout string
	pushes     int
	revParse   string
	branchHit  bool
	descend    bool
	commits    string
	tips       []string
}

func (s *scriptedPushGit) run(_ context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, o)
	s.argvs = append(s.argvs, slices.Clone(args))
	s.mu.Unlock()
	switch args[0] {
	case "init":
		if s.initFail {
			return gitrun.Result{Stderr: []byte(s.initLine + "\n")}, errors.New("exit status 128")
		}
	case "config", "update-ref":
	case "fetch":
		s.mu.Lock()
		s.fetches++
		n := s.fetches
		s.mu.Unlock()
		if n <= s.fetchFail {
			return gitrun.Result{Stderr: []byte(s.fetchLine + "\n")}, errors.New("exit status 128")
		}
	case "rev-parse":
		return gitrun.Result{Stdout: []byte(s.revParse + "\n")}, nil
	case "for-each-ref":
		return s.forEachRef(args), nil
	case "merge-base":
		if !s.descend {
			return gitrun.Result{}, errors.New("exit status 1")
		}
	case "rev-list":
		return gitrun.Result{Stdout: []byte(s.commits + "\n")}, nil
	case "push":
		s.mu.Lock()
		s.pushes++
		n := s.pushes
		s.mu.Unlock()
		if n <= s.pushFail {
			return gitrun.Result{Stdout: []byte(s.pushStdout)}, errors.New("exit status 1")
		}
	}
	return gitrun.Result{}, nil
}

// forEachRef answers the three shapes the pusher asks: --contains (is the head on
// this launch's refs), --format=%(objectname) (the launch's tips for checkoutTip),
// and the drop query (no refs to delete).
func (s *scriptedPushGit) forEachRef(args []string) gitrun.Result {
	if slices.Contains(args, "--contains") {
		if s.branchHit {
			return gitrun.Result{Stdout: []byte("refs/member/" + "x" + "/heads/sprint/c1\n")}
		}
		return gitrun.Result{}
	}
	if slices.Contains(args, "--format=%(objectname)") && len(s.tips) > 0 {
		return gitrun.Result{Stdout: []byte(strings.Join(s.tips, "\n") + "\n")}
	}
	return gitrun.Result{}
}

// A head that is not a sha is refused before any git runs.
// memberpush.go:88-92 (type comment memberpush.go:26; docs/SPEC-CARD-CONTRACT.md section 4).
func TestMemberPushRefusesAHeadThatIsNotASha(t *testing.T) {
	t.Parallel()
	for _, head := range []string{"", "-", "not-a-sha", "12345", "1234567g"} {
		t.Run("head="+head, func(t *testing.T) {
			t.Parallel()
			u := newUnitPush(t)
			got := u.pusher.Push(u.p, member.Result{Head: head})
			assert.Contains(t, got.Refused, "is not a sha")
			assert.Empty(t, got.Sha)
			assert.Empty(t, got.None)
			assert.Empty(t, u.git.argvs, "a head that is not a sha runs no git")
		})
	}
}

// A branch that is not a branch name is refused before any git runs: the refspec
// characters and the name shapes pushBranchRE refuses (memberpush.go:85,93).
func TestMemberPushRefusesABranchThatIsNotABranchName(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{"a..b", "x/", "x.lock", "+main", "a:b", "a b"} {
		t.Run("branch="+branch, func(t *testing.T) {
			t.Parallel()
			u := newUnitPush(t)
			u.p.Branch = branch
			got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
			assert.Contains(t, got.Refused, "is not a branch name")
			assert.Empty(t, got.Sha)
			assert.Empty(t, u.git.argvs, "a branch that is not a branch name runs no git")
		})
	}
}

// A card that names no repository is None, never a refusal.
// memberpush.go:96-102 (docs/SPEC-CARD-CONTRACT.md layer 1).
func TestMemberPushReportsACardThatNamesNoRepository(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.p.Brief = "RESULT: c1\nBASE: main\n\nDo the work.\n"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Equal(t, "the card names no repository", got.None)
	assert.Empty(t, got.Sha)
	assert.Empty(t, u.git.argvs, "no repository: nothing runs")
}

// A card that names a repository but has no checkout is refused by name.
// memberpush.go:103-106.
func TestMemberPushRefusesACardWithNoCheckout(t *testing.T) {
	t.Parallel()
	u := newUnitPushBase(t)
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "no checkout at "+u.checkout)
	assert.Empty(t, got.Sha)
	assert.Empty(t, u.git.argvs, "no checkout: nothing runs")
}

// First use makes the bare push repository, then configures it: no hooks and no
// automatic gc (memberpush.go:87-95, 311-331).
func TestMemberPushInitialisesAndConfiguresThePushRepository(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	require.Equal(t, member.Push{Sha: unitPushHead}, got)
	repo := filepath.Join(u.root, "push.git")
	argvs := u.git.argvs
	require.GreaterOrEqual(t, len(argvs), 4, "init then three configs")
	assert.Equal(t, []string{"init", "-q", "--bare", "--", repo}, argvs[0])
	for i, kv := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}, {"core.hooksPath", os.DevNull}} {
		assert.Equal(t, []string{"config", "--", kv[0], kv[1]}, argvs[1+i])
		assert.Equal(t, repo, u.git.calls[1+i].C, "config runs in push.git")
	}
}

// An init that fails is refused with git's own line.
// memberpush.go:318-322.
func TestMemberPushRefusesAnInitThatFails(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.initFail = true
	u.git.initLine = "fatal: could not create work tree dir 'push.git'"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "the member's push repository")
	assert.Contains(t, got.Refused, "fatal: could not create work tree dir")
	assert.Empty(t, got.Sha)
}

// A fetch that fails every try is refused with the line after waits of 1 s, 2 s
// and 4 s (pushWaits, memberpush.go:77,120-136).
func TestMemberPushRetriesAFailedFetchWithGrowingWaits(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.fetchFail = 4
	u.git.fetchLine = "fatal: bad object refs/member/other/HEAD"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "4 tries")
	assert.Contains(t, got.Refused, "fatal: bad object refs/member/other/HEAD")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, u.sleeps)
	assert.Empty(t, got.Sha)
}

// A fetch that fails once and then answers does not stop the push.
// memberpush.go:120-136.
func TestMemberPushGoesOnAfterAFetchThatFailsOnce(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.fetchFail = 1
	u.git.fetchLine = "fatal: bad object refs/member/other/HEAD"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Equal(t, member.Push{Sha: unitPushHead}, got)
	assert.Equal(t, []time.Duration{time.Second}, u.sleeps)
}

// A head that is not on the launch's branches or HEAD is refused.
// memberpush.go:137-147.
func TestMemberPushRefusesAHeadThatIsNotOnTheCheckoutsBranches(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.branchHit = false
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "is not on this checkout's branches or HEAD")
	assert.Empty(t, got.Sha)
}

// A launch that recorded no staged commit cannot count the child's commits: None.
// memberpush.go:152-153,167-169 (docs/SPEC-CARD-CONTRACT.md section 4).
func TestMemberPushReportsNoStagedCommitRecorded(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.staged("")
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Equal(t, "no staged commit is recorded for this launch to count the child's commits from", got.None)
	assert.Empty(t, got.Sha)
}

// A head that does not descend from the staged commit is refused with both shas.
// memberpush.go:170-172 (tla/CardContract.tla, Push).
func TestMemberPushRefusesAHeadThatDoesNotDescendFromTheStagedCommit(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.descend = false
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "does not descend from the staged commit "+unitPushBase)
	assert.Empty(t, got.Sha)
}

// Zero commits past the staged commit is a child that committed nothing: None.
// memberpush.go:173-179 (docs/SPEC-CARD-CONTRACT.md section 4).
func TestMemberPushReportsAChildThatCommittedNothing(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.commits = "0"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, got.Sha)
}

// git's `[remote rejected]` is the remote's failure: tried again after 1 s, 2 s
// and 4 s, then refused with git's line (memberpush.go:72-77,184-195).
func TestMemberPushResendsAPushTheRemoteRejected(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.pushFail = 4
	u.git.pushStdout = "To " + unitRepoURL + "\n!\trefs/heads/sprint/c1:refs/heads/sprint/c1\t[remote rejected] (failed)\nDone\n"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "[remote rejected]")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, u.sleeps)
	assert.Empty(t, got.Sha)
}

// git's `[rejected]` (not a fast forward) is the commit's: refused at once, never
// waited on, never forced (memberpush.go:74-76,191-192).
func TestMemberPushRefusesANonFastForwardAtOnce(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	u.git.pushFail = 1
	u.git.pushStdout = "To " + unitRepoURL + "\n!\trefs/heads/sprint/c1:refs/heads/sprint/c1\t[rejected] (non-fast-forward)\nDone\n"
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Contains(t, got.Refused, "[rejected]")
	assert.Empty(t, u.sleeps, "a not-a-fast-forward is never waited on")
	assert.Empty(t, got.Sha)
}

// Success is Push{Sha} with the full sha; the push is unforced, from push.git,
// with --no-verify and no terminal prompt (memberpush.go:26-51,184,355-361).
func TestMemberPushPushesTheHeadFromThePushRepositoryUnforced(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead})
	assert.Equal(t, member.Push{Sha: unitPushHead}, got)
	pushRepo := filepath.Join(u.root, "push.git")
	want := []string{"push", "-q", "--porcelain", "--no-verify", "--", unitRepoURL, unitPushHead + ":refs/heads/" + u.p.Branch}
	pos := -1
	for i, a := range u.git.argvs {
		if len(a) > 0 && a[0] == "push" {
			pos = i
			break
		}
	}
	require.NotEqual(t, -1, pos, "the head is pushed")
	assert.Equal(t, want, u.git.argvs[pos])
	for i, o := range u.git.calls {
		assert.NotEqual(t, u.checkout, o.C, "call %d (%v) runs in the checkout", i, u.git.argvs[i])
		assert.True(t, o.C == "" || o.C == pushRepo, "call %d (%v) runs in %q, not push.git or no directory", i, u.git.argvs[i], o.C)
		assert.Contains(t, o.Env, "GIT_TERMINAL_PROMPT=0", "call %d (%v) lacks the no-prompt environment", i, u.git.argvs[i])
	}
}

// A packet with a pull request title and no gh on PATH pushes still; the note
// names the missing gh (memberpush.go:196-204,264-268).
func TestMemberPushNotesNoGHAndStillPushes(t *testing.T) {
	t.Parallel()
	u := newUnitPush(t)
	got := u.pusher.Push(u.p, member.Result{Head: unitPushHead, Title: "a title", Body: "a body"})
	assert.Equal(t, unitPushHead, got.Sha)
	assert.Empty(t, got.PR)
	assert.Contains(t, got.PRNote, "no "+unitNoGH+" on this machine's PATH")
}

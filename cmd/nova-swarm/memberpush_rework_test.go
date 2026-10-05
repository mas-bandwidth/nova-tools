//go:build slow

// Real git repositories and pushes: behind the `slow` tag with slow_test.go's tier, so the unit
// tier stays under its 2 s package budget (internal/ci/slow-tests_allowlist.txt, its header).

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A rework staged at the tip of its base branch finishes from the commit staging made there
// (internal/swarm restageAtTip: the tip with the attempt before's work carried on top): a child
// that continues from it is pushed, and a head on the attempt before's old base is refused for
// not descending from the staged commit. nongo-11 attempt 262 was refused the other way round:
// staged at the old head, it restarted from the tip as its fix asked (nova-tools#5215). The
// member reads where the rework was staged from native's log, for the finish's report.
func TestAReworkStagedAtTheTipFinishesFromItAndNotFromTheOldHead(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	gitAs(t, b.checkout, "switch", "-q", "-c", "attempt1", b.base)
	prev := b.commit(t, "attempt 1\n") // the head attempt 1 pushed, on the old base
	gitAs(t, b.checkout, "switch", "-q", "-C", "rowan/c1", b.base)
	write(t, filepath.Join(b.checkout, "g"), "landed since\n")
	gitAs(t, b.checkout, "add", "g")
	gitAs(t, b.checkout, "commit", "-q", "-m", "landed since")    // the base branch's tip
	gitAs(t, b.checkout, "cherry-pick", "--end-of-options", prev) // attempt 1's work carried onto it
	carried := gitAs(t, b.checkout, "rev-parse", "HEAD")
	b.staged(t, carried)

	gitAs(t, b.checkout, "switch", "-q", "attempt1")
	stale := b.commit(t, "fixed on the old base\n")
	gitAs(t, b.checkout, "switch", "-q", "rowan/c1")
	got := b.pusher().Push(b.p, member.Result{Head: stale})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "does not descend from the staged commit "+carried)

	head := b.commit(t, "fixed on the tip\n")
	assert.Equal(t, member.Push{Sha: head}, b.pusher().Push(b.p, member.Result{Head: head}), "a head from the staged commit is pushed")
	assert.Equal(t, head, b.originHas(t, b.p.Branch))

	carry := cardcontract.Carry{Base: "main", Tip: gitAs(t, b.checkout, "rev-parse", carried+"^"), Prev: prev, From: 1, Staged: carried, State: cardcontract.CarryOK}
	done := make(chan struct{})
	close(done)
	logPath := filepath.Join(b.root, "c1.native.log")
	write(t, logPath, "STAGE OK bench=b repo=r base=x secs=1 clone=0.1 fetch=0.1 checkout=0.1\n"+carry.Line()+"\nFRAME OK secs=0.1\n")
	c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(b.root, "results"), job: filepath.Join(b.root, "job"), done: done}
	assert.Equal(t, carry.Words(), c.Result().Carry, "the finish's report is told where the rework was staged")
}

// A carried rework's staged commit is the attempt before's head: a commit origin holds and
// the checkout started from. The member's push repository holds it itself once the checkout
// is fetched, and borrows nothing from the bench mirror, which is refreshed under it. On
// 2026-10-03 (tokens-version-go attempt 3, rerate2-use-redis attempt 2) the push repository
// read the staged commit through the mirror's objects, the mirror lost it between the fetch
// and the count (the attempt before's branch gone with a refresh), and a correct carry was
// refused "does not descend from the staged commit: fatal: Not a valid commit name <staged>".
// Here the mirror is origin itself (a card naming a repository on disk), and it loses the
// staged commit after the fetch and before the count.
func TestACarriedReworksStagedCommitIsThePushRepositorysOwn(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	prev := b.commit(t, "attempt 1\n")
	gitAs(t, b.checkout, "push", "-q", "origin", prev+":refs/heads/sprint/c1.a1") // attempt 1's push
	b.staged(t, prev)                                                             // attempt 2 starts from it
	head := b.commit(t, "attempt 2\n")
	g := b.pusher()
	real, lost := g.git, false
	g.git = func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
		if args[0] == "merge-base" && !lost {
			lost = true
			runGit(t, b.origin, "branch", "-q", "-D", "sprint/c1.a1")
			runGit(t, b.origin, "gc", "-q", "--prune=now")
		}
		return real(ctx, o, args...)
	}
	assert.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	require.True(t, lost, "the mirror lost the staged commit before the count")
	assert.Equal(t, head, b.originHas(t, b.p.Branch))
	assert.NoFileExists(t, filepath.Join(b.root, "push.git", "objects", "info", "alternates"), "the push repository borrows no objects")
}

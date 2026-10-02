package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pruneRig is the land rig whose cards record their branches, as a member's finish does.
func pruneRig(t *testing.T) *landRig {
	t.Helper()
	r := newLandRig(t)
	r.branch = func(id string) string { return "sprint/" + id }
	return r
}

// originBranches is origin's branches, sorted.
func (r *landRig) originBranches() []string {
	r.t.Helper()
	out := strings.Fields(r.git(r.remote, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
	slices.Sort(out)
	return out
}

// pushTrace is the push commands git ran under GIT_TRACE in the file trace, in order.
func pushTrace(t *testing.T, trace string) []string {
	t.Helper()
	b, err := os.ReadFile(trace)
	require.NoError(t, err)
	var pushes []string
	for _, line := range strings.Split(string(b), "\n") {
		if _, cmd, ok := strings.Cut(line, "trace: built-in: git push "); ok {
			pushes = append(pushes, strings.ReplaceAll(cmd, "'", ""))
		}
	}
	return pushes
}

// A landed and reported batch's cards' branches are deleted from origin, in one push after
// the landing's own: the base holds the batch, a branch that is no card's stays, and the
// LAND line says how many were queued, the PRUNE line how many went.
func TestLandDeletesTheLandedCardsBranchesFromOriginAfterTheBatch(t *testing.T) {
	t.Parallel()
	r := pruneRig(t)
	landTwo(r)
	r.git(r.worker, "push", "-q", "origin", "refs/remotes/origin/main:refs/heads/other")
	trace := filepath.Join(r.dir, "git-trace")
	r.a.gitEnv = append(slices.Clone(r.env), "GIT_TRACE="+trace)
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Regexp(t, `LAND OK stream=s1 cards=2 base=main .* report=\d+\.\ds branches_queued=2\n`, out)
	assert.Regexp(t, `PRUNE OK branches=2 refs=0 dir=`+r.clone+` took=\d+\.\ds\n`, out)
	assert.Less(t, strings.Index(out, "PRUNE OK"), strings.Index(out, "LAND DONE"))
	assert.Equal(t, []string{"main", "other"}, r.originBranches())
	assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog())
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	tip := r.git(r.remote, "rev-parse", "main")
	assert.Equal(t, []string{
		"--porcelain origin " + tip + ":refs/heads/main",
		"--porcelain --no-verify origin :refs/heads/sprint/s1-1 :refs/heads/sprint/s1-2",
	}, pushTrace(t, trace), "the landing's push, then one delete push naming each card's branch")
	assert.Zero(t, r.a.prune.waiting())

	// --json carries the queued count on the batch and the cleanup beside the items
	r = pruneRig(t)
	landTwo(r)
	var v struct {
		Items []landBatch   `json:"items"`
		Prune []pruneResult `json:"prune"`
	}
	r.json("land --repo-dir "+r.clone+" --base main", &v)
	require.Len(t, v.Items, 1)
	require.NotNil(t, v.Items[0].Prune)
	assert.Equal(t, 2, v.Items[0].Prune.Queued)
	require.Len(t, v.Prune, 1)
	assert.Equal(t, pruneResult{Status: "ok", Dir: r.clone, Branches: 2, Took: v.Prune[0].Took}, v.Prune[0])
	assert.Equal(t, []string{"main"}, r.originBranches())
}

// In the land loop the landing only tags: a round that lands pushes no delete and
// leaves the branches on origin, queued; the next round with nothing queued to merge
// deletes them.
func TestTheLandLoopDeletesBranchesLazilyBetweenRounds(t *testing.T) {
	t.Parallel()
	r := pruneRig(t)
	landTwo(r)
	trace := filepath.Join(r.dir, "git-trace")
	r.a.gitEnv = append(slices.Clone(r.env), "GIT_TRACE="+trace)
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	var out bytes.Buffer
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.Contains(t, out.String(), "branches_queued=2")
	assert.NotContains(t, out.String(), "PRUNE")
	assert.Equal(t, []string{"main", "sprint/s1-1", "sprint/s1-2"}, r.originBranches(), "the landing's round deletes nothing")
	for _, p := range pushTrace(t, trace) {
		assert.NotContains(t, p, " :refs/heads/", "a landing's steps hold no delete push")
	}
	assert.Equal(t, 2, r.a.prune.waiting())

	out.Reset()
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out))
	assert.Regexp(t, `^\d\d:\d\d:\d\d PRUNE OK branches=2 refs=0 dir=`+r.clone+` took=\d+\.\ds\n$`, out.String())
	assert.Equal(t, []string{"main"}, r.originBranches())
	assert.Zero(t, r.a.prune.waiting())
}

// A delete origin refuses never fails the landing: the batch is landed and reported, the
// branches stay on origin and the PRUNE line says so; the loop keeps them queued, waits
// PruneRetry, and deletes them once origin takes it.
func TestARefusedDeleteLeavesTheBatchLandedAndTheBranchesQueued(t *testing.T) {
	t.Parallel()
	r := pruneRig(t)
	landTwo(r)
	r.git(r.remote, "config", "receive.denyDeletes", "true")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=2")
	assert.Contains(t, out, "PRUNE FAILED branches=0 refs=0 dir="+r.clone)
	assert.Contains(t, out, "left=2 reason=origin refused the delete of sprint/s1-1")
	assert.Contains(t, out, "; they stay on origin\n")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Equal(t, []string{"main", "sprint/s1-1", "sprint/s1-2"}, r.originBranches())

	r = pruneRig(t)
	landTwo(r)
	r.git(r.remote, "config", "receive.denyDeletes", "true")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	var lines bytes.Buffer
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &lines))
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &lines))
	assert.Contains(t, lines.String(), "PRUNE FAILED branches=0 refs=0")
	assert.Contains(t, lines.String(), "they stay queued and the cleanup is tried again")
	assert.Equal(t, 2, r.a.prune.waiting())
	lines.Reset()
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &lines))
	assert.Empty(t, lines.String(), "a failed cleanup waits out PruneRetry")
	r.git(r.remote, "config", "receive.denyDeletes", "false")
	r.a.sleep(PruneRetry)
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &lines))
	assert.Contains(t, lines.String(), "PRUNE OK branches=2")
	assert.Equal(t, []string{"main"}, r.originBranches())

	// a branch origin no longer holds is deleted all the same
	r.a.prune.add(r.clone, "main", []string{"sprint/gone"})
	res := r.a.flushPrune(context.Background(), false)
	require.Len(t, res, 1)
	assert.Equal(t, "ok", res[0].Status, res[0].Reason)
	assert.Equal(t, 1, res[0].Branches)
}

// A batch pushed and not reported keeps its branches (land is run again and may need the
// heads); the run that recovers it deletes the branch of the card it records, and the card
// taken back keeps its own.
func TestABatchPushedAndNotReportedKeepsItsBranches(t *testing.T) {
	t.Parallel()
	r := pruneRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	r.a.beforePush = func(int) { r.ok("return s1-1 --reason 'taken back under the push'") }
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "LAND FAILED stream=s1 cards=2")
	assert.NotContains(t, out+errs, "branches_queued")
	assert.NotContains(t, out+errs, "PRUNE")
	assert.Equal(t, []string{"main", "sprint/s1-1", "sprint/s1-2"}, r.originBranches())
	r.a.beforePush = nil
	out = r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1")
	assert.Contains(t, out, "PRUNE OK branches=1")
	assert.Equal(t, []string{"main", "sprint/s1-1"}, r.originBranches())
}

// The base is never deleted, nor any branch that is not one the sprint names: a card
// recording the base, another branch or an option-like name is said and kept.
func TestLandNeverDeletesTheBaseOrABranchTheSprintDoesNotName(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	names := map[string]string{"s1-1": "main", "s1-2": "other", "s1-3": "-x"}
	r.branch = func(id string) string { return names[id] }
	r.git(r.worker, "push", "-q", "origin", "refs/remotes/origin/main:refs/heads/other")
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=3")
	assert.Contains(t, out, "branches_queued=0 branches_kept=3")
	assert.Contains(t, out, "NOTE no branch queued for deletion for s1-1.w1: its branch main is the base")
	assert.Contains(t, out, "NOTE no branch queued for deletion for s1-2.w1: its branch other is not one the sprint names")
	assert.Contains(t, out, "NOTE no branch queued for deletion for s1-3.w1: its branch -x is not a branch name")
	assert.Equal(t, []string{"land s1-3 (sprint stream s1)", "land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog())
	assert.Equal(t, []string{"main", "other", "sprint/s1-1", "sprint/s1-2", "sprint/s1-3"}, r.originBranches())
	for _, tc := range []struct{ branch, base, want string }{
		{"", "main", "it records no branch"},
		{"main", "main", "is the base"},
		{"sprint/a:refs/heads/main", "main", "not one the sprint names"},
		{"sprint/a..b", "main", "not one the sprint names"},
		{"sprint/a.lock", "main", "not one the sprint names"},
		{"sprint/a+b", "main", "not one the sprint names"},
	} {
		assert.Contains(t, pruneWhy(tc.branch, tc.base), tc.want, tc.branch)
	}
	assert.Empty(t, pruneWhy("sprint/s1-1.w1.g1.e13", "main"))
}

// The dry run deletes nothing and queues nothing: it says how many branches the real run
// would queue.
func TestLandDryRunDeletesNothing(t *testing.T) {
	t.Parallel()
	r := pruneRig(t)
	landTwo(r)
	out := r.ok("land --repo-dir " + r.clone + " --base main --dry-run")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2 dir="+r.clone+" branches_queued=2 dry_run=yes")
	assert.NotContains(t, out, "PRUNE")
	assert.Zero(t, r.a.prune.waiting())
	assert.Equal(t, []string{"main", "sprint/s1-1", "sprint/s1-2"}, r.originBranches())
}

// A reworked card's attempts are work cards of their own, each recording its own branch:
// the landing deletes every attempt's.
func TestLandDeletesTheBranchOfEveryAttemptOfACard(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.branch = func(id string) string { return "sprint/" + id + ".w1" }
	r.ok("add --stream s1 --count 1")
	first := r.head("s1-1.w1", "main", "a.txt", "attempt 1\n")
	r.queued(map[string]string{"s1-1": first}, "s1-1")
	r.ok("return s1-1 --reason 'again'")
	r.ok("rework s1-1 --fix 'again'")
	second := r.head("s1-1.w2", "main", "b.txt", "attempt 2\n")
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
	var q struct{ Cards []queueCard }
	r.json("queue --as m1", &q)
	if len(q.Cards) == 0 {
		r.deal(1)
		r.json("queue --as m1", &q)
	}
	require.Len(t, q.Cards, 1)
	w := q.Cards[0].ID + "@" + strconv.Itoa(q.Cards[0].Gen)
	if q.Cards[0].Col == "ready" {
		r.ok("take --as m1 " + w)
	}
	r.ok("finish --as m1 " + w + " --head " + second + " --branch sprint/s1-1.w2")
	r.ok("ask")
	r.ok("read --as reader-a --ok --limit 100")
	r.ok("read --as reader-b --ok --limit 100")
	r.ok("accept --read-ok")
	assert.Equal(t, []string{"main", "sprint/s1-1.w1", "sprint/s1-1.w2"}, r.originBranches())
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1")
	assert.Contains(t, out, "branches_queued=2")
	assert.Contains(t, out, "PRUNE OK branches=2")
	assert.Equal(t, []string{"main"}, r.originBranches())
	assert.Equal(t, "attempt 2", r.git(r.remote, "show", "main:b.txt"))
}

// The cleanup removes the clone's remote-tracking refs of branches origin no longer holds;
// the base's ref and the refs of branches origin still holds stay.
func TestTheCleanupRemovesTheClonesStaleRemoteTrackingRefs(t *testing.T) {
	t.Parallel()
	r := pruneRig(t)
	landTwo(r)
	for _, b := range []string{"other", "stale"} {
		r.git(r.worker, "push", "-q", "origin", "refs/remotes/origin/main:refs/heads/"+b)
	}
	r.git(r.clone, "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
	r.git(r.worker, "push", "-q", "origin", ":refs/heads/stale")
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	// the delete push drops the refs of the branches it deletes (this clone tracks every
	// branch); the stale one is the cleanup's
	assert.Contains(t, out, "PRUNE OK branches=2 refs=1 dir="+r.clone)
	refs := strings.Fields(r.git(r.clone, "for-each-ref", "--format=%(refname)", "refs/remotes/origin"))
	assert.Contains(t, refs, "refs/remotes/origin/main")
	assert.Contains(t, refs, "refs/remotes/origin/other")
	assert.NotContains(t, refs, "refs/remotes/origin/stale")
	assert.NotContains(t, refs, "refs/remotes/origin/sprint/s1-1")
	assert.Equal(t, []string{"main", "other"}, r.originBranches())
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func queueOneLandCard(r *landRig) {
	r.t.Helper()
	r.ok("add --stream s1 --count 1 --one")
	head := r.head("s1-1", "main", "card.txt", "card\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")
}

func pushDevChange(r *landRig, file, text string) {
	r.t.Helper()
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "dev", "refs/remotes/origin/main")
	r.commit(file, text, "development change")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/dev")
}

func TestLandDevSyncIsOptIn(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)

	out := r.ok("land --repo-dir " + r.clone + " --base main --check true")
	assert.Contains(t, out, "LAND DONE batches=1 cards=1 refused=0")
	assert.NotContains(t, r.mainLog(), "land dev sync: merge dev into main", "ordinary land does not sync dev")
	r.clean()
}

func TestLandDevSyncRunsBeforeTheFirstBatchAndRecordsTheSyncedSHA(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)

	out := r.ok("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1")
	assert.Contains(t, r.git(r.remote, "show", "main:dev-only.txt"), "dev")
	assert.Contains(t, r.git(r.remote, "show", "main:card.txt"), "card")
	assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "land dev sync: merge dev into main", "base"}, r.mainLog())
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Merge}, nil)
	require.NoError(t, err)
	sha, ok := s.Merge.Prop(sprint.PropDevSyncSha)
	assert.True(t, ok, "the cycle records the exact dev-sync tip")
	assert.Equal(t, "land dev sync: merge dev into main", r.git(r.remote, "show", "-s", "--format=%s", sha))
	r.clean()
}

func TestLandDevSyncConflictStopsBeforeAnyBatch(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "conflict.txt", "dev\n")
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "--detach", "refs/remotes/origin/main")
	r.commit("conflict.txt", "base\n", "base conflict change")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	queueOneLandCard(r)
	before := r.git(r.remote, "rev-parse", "main")

	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "dev sync conflict")
	assert.Contains(t, errs, "no landing batch was started")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "the conflict is aborted and no card batch is pushed")
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"])
	assert.Contains(t, r.streamState("s1"), sprint.DevSyncCause)
	open, err := r.m.OpenNotes(context.Background())
	require.NoError(t, err)
	var conflicts int
	for _, note := range open {
		if note.Note.Type == sprint.NDevSyncConflict {
			conflicts++
		}
	}
	assert.Equal(t, 1, conflicts, "the sync conflict is recorded once")
	r.clean()
}

func TestLandDevSyncRedTreeGateDoesNotRecordOrStartABatch(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)
	before := r.git(r.remote, "rev-parse", "main")
	check := filepath.Join(r.dir, "red-sync-check.sh")
	require.NoError(t, os.WriteFile(check, []byte("exit 1\n"), 0o600))

	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check 'sh " + check + "' --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "dev sync: the tree gate")
	assert.Contains(t, errs, "no landing batch was started")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "the ungated merge is reset and not pushed")
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Merge}, nil)
	require.NoError(t, err)
	_, recorded := s.Merge.Prop(sprint.PropDevSyncSha)
	assert.False(t, recorded, "a red sync is not recorded")
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"])
	r.clean()
}

func TestLandDevSyncRejectsDirtyExplicitCloneBeforeRedGateCanResetIt(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)
	dirty := filepath.Join(r.clone, "README")
	require.NoError(t, os.WriteFile(dirty, []byte("preserve me\n"), 0o600))
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check false --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "explicit clone is not clean")
	contents, err := os.ReadFile(dirty)
	require.NoError(t, err)
	assert.Equal(t, "preserve me\n", string(contents))
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	r.clean()
}

func TestLandDevSyncRejectsExplicitCloneWithMergeInProgress(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)
	r.git(r.clone, "switch", "-q", "-c", "held-merge")
	require.NoError(t, os.WriteFile(filepath.Join(r.clone, "merge.txt"), []byte("ours\n"), 0o600))
	r.git(r.clone, "add", "merge.txt")
	r.git(r.clone, "commit", "-q", "-m", "ours")
	r.git(r.clone, "switch", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(r.clone, "merge.txt"), []byte("theirs\n"), 0o600))
	r.git(r.clone, "add", "merge.txt")
	r.git(r.clone, "commit", "-q", "-m", "theirs")
	_, err := gitrun.Run(context.Background(), gitrun.Options{C: r.clone, Env: r.env, OwnRepo: true}, "merge", "--no-commit", "held-merge")
	require.Error(t, err, "the merge must remain unresolved")
	mergeHead := r.git(r.clone, "rev-parse", "--verify", "MERGE_HEAD")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check false --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "explicit clone has an in-progress merge")
	assert.Equal(t, mergeHead, r.git(r.clone, "rev-parse", "--verify", "MERGE_HEAD"))
	assert.Contains(t, r.git(r.clone, "status", "--porcelain"), "merge.txt")
	r.clean()
}

func TestLandDevSyncRefusesProtectedBaseBeforeSyncPush(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)
	r.ok("stream set s1 --land-protected default")
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "a protected branch")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "authorization refusal precedes any sync push")
	r.clean()
}

func TestLandDevSyncHonorsMergeQueuePauseBeforeSyncPush(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)
	r.queue.hold("main", true)
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "merge queue of main holds a group")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "pause precedes sync push")
	r.clean()
}

func TestLandDevSyncRechecksMergeQueueImmediatelyBeforePush(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "dev-only.txt", "dev\n")
	queueOneLandCard(r)
	r.queue.mu.Lock()
	r.queue.from = 2 // first ask is preflight; hold begins for the push-time recheck
	r.queue.mu.Unlock()
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "merge queue of main holds a group")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "the sync merge was not pushed after the queue changed")
	r.clean()
}

func TestLandDevSyncRetryIncludesConflictStoppedStreamAndRecomputesOrder(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	pushDevChange(r, "conflict.txt", "dev\n")
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "--detach", "refs/remotes/origin/main")
	r.commit("conflict.txt", "base\n", "base conflict change")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	queueOneLandCard(r)
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "dev sync conflict")
	assert.Contains(t, r.streamState("s1"), sprint.DevSyncCause)
	// Resolve the dev/base conflict by merging the current base into dev and
	// choosing the base's version. The same unqualified land command must retry.
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "dev")
	_, mergeErr := gitrun.Run(context.Background(), gitrun.Options{C: r.worker, Env: r.env, OwnRepo: true}, "merge", "--no-ff", "-m", "resolve", "origin/main")
	require.Error(t, mergeErr, "the histories still conflict before manual resolution")
	r.git(r.worker, "checkout", "--theirs", "--", "conflict.txt")
	r.git(r.worker, "add", "conflict.txt")
	r.git(r.worker, "commit", "-q", "-m", "resolve conflict")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/dev")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check true --dev-sync")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=1", "the stream resumes and is reconsidered in the same command")
	assert.Equal(t, "landed/merged", r.places("s1-1")["s1-1"])
	r.clean()
}

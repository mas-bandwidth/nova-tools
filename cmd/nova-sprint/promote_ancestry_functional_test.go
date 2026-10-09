package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/stretchr/testify/require"
)

func TestFindPromotionPRRequiresTheExactRepositoryHeadAndBase(t *testing.T) {
	t.Parallel()
	p := &promoter{ghRun: func(_ context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		if strings.HasPrefix(call, "repo view ") {
			return "owner/repo", nil
		}
		if strings.HasPrefix(call, "pr list ") {
			return `[{"number":1,"headRefName":"promo/cut","baseRefName":"dev","headRepository":{"nameWithOwner":"owner/repo"},"headRepositoryOwner":{"login":"owner"}},` +
				`{"number":2,"headRefName":"promo/cut","baseRefName":"dev","headRepository":{"nameWithOwner":"other/repo"},"headRepositoryOwner":{"login":"other"}},` +
				`{"number":3,"headRefName":"promo/cut","baseRefName":"main","headRepository":{"nameWithOwner":"owner/repo"},"headRepositoryOwner":{"login":"owner"}}]`, nil
		}
		return "", errors.New("unexpected gh call: " + call)
	}}
	matches, err := (ghForge{p: p}).FindPR(context.Background(), "dev", "promo/cut")
	require.NoError(t, err)
	require.Equal(t, []string{"1"}, matches)
}

type lostCreatePromotionForge struct {
	*fakeForge
	lost bool
}

type movingAtEnqueueForge struct {
	*fakeForge
	before func(string)
}

func (f *movingAtEnqueueForge) Enqueue(ctx context.Context, id, expectedHeadOID string) (string, error) {
	f.before(f.head)
	return f.fakeForge.Enqueue(ctx, id, expectedHeadOID)
}

func (f *lostCreatePromotionForge) OpenPR(ctx context.Context, base, head, title, body string) (string, error) {
	number, err := f.fakeForge.OpenPR(ctx, base, head, title, body)
	if err != nil {
		return "", err
	}
	if !f.lost {
		f.lost = true
		return "", errors.New("the forge applied the PR create but its answer was lost")
	}
	return number, nil
}

type movingPromotionForge struct {
	*fakeForge
	beforePass func(string)
}

func (f *movingPromotionForge) Checks(ctx context.Context, number string) ([]promoteCheck, error) {
	checks, err := f.fakeForge.Checks(ctx, number)
	if f.looks == 2 && f.beforePass != nil {
		f.beforePass(f.head)
	}
	return checks, err
}

func (w *promoteTwin) mergeOnDev(head string, deleteBranch bool) string {
	w.t.Helper()
	w.git(w.peer, "fetch", "-q", "origin", "+refs/heads/dev:refs/remotes/origin/dev", "+refs/heads/"+head+":refs/remotes/origin/"+head)
	w.git(w.peer, "checkout", "-q", "-B", "dev", "origin/dev")
	w.git(w.peer, "merge", "-q", "--no-ff", "-m", "merge tested promotion", "refs/remotes/origin/"+head)
	merged := w.git(w.peer, "rev-parse", "HEAD")
	w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/dev")
	if deleteBranch {
		w.git(w.peer, "push", "-q", "origin", ":refs/heads/"+head)
	}
	return merged
}

// A forge's merged flag cannot replace the destination branch's Git ancestry.
// The bare origin is the real destination; the fake forge deliberately reports
// a promoted head without moving dev at all.
func TestPromoteMergedFlagCannotRecordAHeadAbsentFromDev(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
	f.merged = func(head string) string { return w.remote("refs/heads/" + head) }
	useForge(t, w, f)
	ta := promoteApp(t, w)
	devBefore := w.remote("refs/heads/dev")
	code, out, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Equal(t, 1, code, "%s\n%s", out, errs)
	require.Equal(t, devBefore, w.remote("refs/heads/dev"), "the forge did not put the cut on dev")
	require.Empty(t, ta.promotedSha(), "no store promotion before destination ancestry is proved")
	require.Empty(t, w.git(w.dir, "for-each-ref", "--format=%(objectname)", "refs/promoted/last"), "no local promoted marker before destination ancestry is proved")
	require.Contains(t, errs, "origin/dev")
	merged := w.mergeOnDev(f.head, false)
	f.mergedSHA = merged
	code, out, errs = ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Zero(t, code, "%s\n%s", out, errs)
	require.Equal(t, merged, ta.promotedSha(), "the in-flight PR can be recorded when dev later proves the merge")
	require.Equal(t, merged, w.git(w.dir, "rev-parse", "refs/promoted/last"))
	require.Equal(t, 1, countPromotionCall(f.calls, "open "), "retry uses the existing PR")
	ta.clean()
}

func countPromotionCall(calls []string, prefix string) int {
	n := 0
	for _, call := range calls {
		if len(call) >= len(prefix) && call[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

// A merged pull request may have its head branch deleted by the forge. The
// destination's actual tip and the frozen cut still prove the landing.
func TestPromoteRecordsActualDevMergeAfterPRBranchDeletion(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
	f.merged = func(head string) string { return w.mergeOnDev(head, true) }
	useForge(t, w, f)
	ta := promoteApp(t, w)
	code, out, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Zero(t, code, "%s\n%s", out, errs)
	dev := w.remote("refs/heads/dev")
	require.NotEmpty(t, dev)
	require.Equal(t, dev, ta.promotedSha(), "record only the commit on actual dev")
	require.Equal(t, dev, w.git(w.dir, "rev-parse", "refs/promoted/last"))
	require.Empty(t, w.remote("refs/heads/"+f.head), "postmerge PR branch deletion is safe")
	ta.clean()
}

func TestPromoteRefusesAChangedPRHeadBeforeEnqueue(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	f := &movingPromotionForge{fakeForge: &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}}
	f.beforePass = func(head string) {
		ref := "refs/remotes/origin/" + head
		w.git(w.peer, "fetch", "-q", "origin", "+refs/heads/"+head+":"+ref)
		w.git(w.peer, "checkout", "-q", "-B", head, ref)
		w.commit(w.peer, "unchecked", "not gated\n", "unchecked head movement")
		w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/"+head)
	}
	useForge(t, w, f)
	ta := promoteApp(t, w)
	code, out, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Equal(t, 1, code, "%s\n%s", out, errs)
	require.Contains(t, errs, "no longer equals gated cut")
	require.False(t, f.did("enqueue"), "checks for an older head cannot admit the changed head")
	require.Empty(t, ta.promotedSha())
	ta.clean()
}

// A head changed after the pre-enqueue read can still contain the gated cut.
// Destination ancestry alone would accept that extra unchecked commit; the
// merged PR's head OID must also equal the cut, even after the queue merges.
func TestPromoteRefusesAnUncheckedHeadMergedAfterEnqueue(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
	f.merged = func(head string) string {
		ref := "refs/remotes/origin/" + head
		w.git(w.peer, "fetch", "-q", "origin", "+refs/heads/"+head+":"+ref)
		w.git(w.peer, "checkout", "-q", "-B", head, ref)
		w.commit(w.peer, "unchecked", "not gated\n", "unchecked head after enqueue")
		w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/"+head)
		return w.mergeOnDev(head, false)
	}
	useForge(t, w, f)
	ta := promoteApp(t, w)
	code, _, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Equal(t, 1, code)
	require.Contains(t, errs, "differs from gated cut")
	require.True(t, f.did("enqueue"))
	require.Empty(t, ta.promotedSha())
	require.Empty(t, w.git(w.dir, "for-each-ref", "--format=%(objectname)", "refs/promoted/last"))
	ta.clean()
}

func TestPromoteEnqueueAtomicallyRejectsAHeadMovedAfterLocalCheck(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	f := &movingAtEnqueueForge{fakeForge: &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}}
	f.before = func(head string) {
		ref := "refs/remotes/origin/" + head
		w.git(w.peer, "fetch", "-q", "origin", "+refs/heads/"+head+":"+ref)
		w.git(w.peer, "checkout", "-q", "-B", head, ref)
		w.commit(w.peer, "unchecked", "not gated\n", "unchecked head between check and enqueue")
		w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/"+head)
	}
	useForge(t, w, f)
	ta := promoteApp(t, w)
	code, _, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Equal(t, 1, code)
	require.Contains(t, errs, "enqueue rejected changed pull request head")
	require.Empty(t, f.entry, "expectedHeadOid rejects before a merge-queue entry exists")
	require.Empty(t, ta.promotedSha())
	require.Empty(t, w.git(w.dir, "for-each-ref", "--format=%(objectname)", "refs/promoted/last"))
	ta.clean()
}

// The first process loses the answer after the forge has created the PR.
// A second invocation recovers by the saved exact base/head intent, keeps the
// same cut and PR, and only then watches the actual destination merge.
func TestPromoteRecoversALostPRCreateAnswerWithoutAnotherPR(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	f := &lostCreatePromotionForge{fakeForge: &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}}
	f.merged = func(head string) string { return w.mergeOnDev(head, false) }
	useForge(t, w, f)
	ta := promoteApp(t, w)
	code, _, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Equal(t, 1, code)
	require.Contains(t, errs, "creation is uncertain")
	branch := w.git(w.dir, "config", "--local", "--get", "promote.branch")
	cut := w.git(w.dir, "config", "--local", "--get", "promote.cut")
	require.Equal(t, "dev", w.git(w.dir, "config", "--local", "--get", "promote.base"))
	_, missingErr := gitrun.Run(context.Background(), gitrun.Options{C: w.dir, Env: w.env}, "config", "--local", "--get", "promote.pr")
	require.Error(t, missingErr)
	require.Equal(t, 1, countPromotionCall(f.calls, "open "))
	code, out, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
	require.Zero(t, code, "%s\n%s", out, errs)
	require.Contains(t, out, "PROMOTE RECOVERED branch="+branch+" pr=5351")
	require.Equal(t, 1, countPromotionCall(f.calls, "open "), "restart must not create a second PR")
	require.Equal(t, 1, countPromotionCall(f.calls, "find "))
	require.Equal(t, cut, w.git(w.dir, "merge-base", w.remote("refs/heads/dev"), cut))
	require.Equal(t, w.remote("refs/heads/dev"), ta.promotedSha())
	ta.clean()
}

func TestPromoteHoldsLegacyPendingPRWithoutSavedBase(t *testing.T) {
	t.Parallel()
	w := newPromoteTwin(t)
	w.inFlight(w.remote("refs/heads/sprint/live"), false)
	w.git(w.dir, "config", "--local", "--unset", "promote.base")
	f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
	useForge(t, w, f)
	ta := promoteApp(t, w)
	code, _, errs := ta.do("promote --once --branch sprint/live --base dev --repo-dir " + w.dir)
	require.Equal(t, 1, code)
	require.Contains(t, errs, "no saved base")
	require.False(t, f.did("enqueue"))
	require.Empty(t, ta.promotedSha())
	ta.clean()
}

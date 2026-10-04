package main

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

// A real local work-branch push cannot produce a dev-landed label or branch
// cleanup. This test's check guards the staged candidate's actual file.
func TestPromotionWorkBranchStageKeepsWorkAndDependencies(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	head := r.head("s1-1", "main", "one.txt", "one\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check 'test -f one.txt'")
	require.Equal(t, 0, code, out+errs)
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"])
	assert.Contains(t, out, "delivery=staged")
	assert.NotContains(t, out, "delivery=dev-landed")
	assert.NotContains(t, out, "branches_queued=")
	code, out, errs = r.do("promote --stream s1 --dry-run")
	require.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "inventory_sha256=")
	assert.Contains(t, out, "target=dev")
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"])
}

func TestPromotionCannotUseLandToDirectPushDev(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "one.txt", "one\n")}, "s1-1")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base dev --check 'test -f one.txt'")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "reviewed GitHub queue")
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"])
}

func TestPromotionPrepareUsesExistingMergeChecksAndChangesNoLifecycle(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "push", "-q", "origin", "refs/heads/main:refs/heads/dev")
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "one.txt", "one\n")}, "s1-1")
	r.ok("land --repo-dir " + r.clone + " --base main --check 'test -f one.txt'")
	before := r.git(r.clone, "ls-remote", "origin", "refs/heads/dev")
	code, out, errs := r.do("promote --stream s1 --repo-dir " + r.clone + " --prepare --check 'test -f one.txt' --json")
	require.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "review_template")
	assert.Contains(t, out, "candidate_tip")
	assert.Equal(t, before, r.git(r.clone, "ls-remote", "origin", "refs/heads/dev"), "prepare never pushes dev")
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"])
}

// Recovery keeps the entire frozen reviewed batch available for ancestry
// verification even if the store recorded its first card before a crash.
func TestPromotionRecoversPartialReceiptAndRejectsReturnedStage(t *testing.T) {
	t.Parallel()
	raw := []byte("frozen exact review")
	hash := sha256.Sum256(raw)
	candidate := strings.Repeat("c", 40)
	head := strings.Repeat("a", 40)
	s := &sprint.Snapshot{Epoch: 7, Work: sprint.NewTable(sprint.Work), Merge: sprint.NewTable(sprint.Merge)}
	review := promotionReview{Candidate: candidate, Entries: []sprint.PinnedCard{{ID: "one", Head: head, Attempt: "1"}, {ID: "two", Head: head, Attempt: "1"}}}
	s.Work.Put(&sprint.Card{ID: "one", Row: "stream", Col: sprint.Landed, Fields: map[string]string{"head": head, "attempt": "1", "dev_head": head, "dev_head_pin": head, "dev_attempt": "1", "dev_epoch": "7", "dev_repo": "owner/repo", "dev_branch": "dev", "dev_tip": candidate, "dev_candidate_tip": candidate, "dev_ci_tip": candidate, "dev_ci": "exact check", "dev_review": "sha256:" + hex.EncodeToString(hash[:]) + "; queue PR", "dev_batch": "batch", "dev_verified_at": time.Now().UTC().Format(time.RFC3339)}})
	s.Work.Put(&sprint.Card{ID: "two", Row: "stream", Col: sprint.Merging, Fields: map[string]string{"head": head, "attempt": "1", "staged_head": head, "staged_head_pin": head, "staged_attempt": "1", "staged_repo": "owner/repo", "staged_returns": "0", "returns": "0"}})
	s.Merge.Put(&sprint.Card{ID: "two", Row: "stream", Col: sprint.Queued})
	pins, replay, why := reviewedPromotionPins(s, "stream", review, raw)
	require.Empty(t, why)
	require.Len(t, pins, 2, "both exact original pins remain available for Git ancestry verification")
	assert.False(t, replay, "remaining merging card still requires its receipt")
	s.Work.Card("two").Fields["returns"] = "1"
	_, _, why = reviewedPromotionPins(s, "stream", review, raw)
	assert.NotEmpty(t, why, "return and reaccept of same head/attempt invalidates staged control proof")
}

func TestPromotionReviewCannotSkipOrReverseTheQueuedPrefix(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	s := &sprint.Snapshot{Epoch: 7, Work: sprint.NewTable(sprint.Work), Merge: sprint.NewTable(sprint.Merge)}
	for i, id := range []string{"one", "two"} {
		s.Work.Put(&sprint.Card{ID: id, Row: "stream", Col: sprint.Merging, Score: float64(i), Fields: map[string]string{"head": head, "attempt": "1", "staged_head": head, "staged_head_pin": head, "staged_attempt": "1", "staged_repo": "owner/repo"}})
		s.Merge.Put(&sprint.Card{ID: id, Row: "stream", Col: sprint.Queued, Score: float64(i)})
	}
	pin := func(id string) sprint.PinnedCard { return sprint.PinnedCard{ID: id, Head: head, Attempt: "1"} }
	for _, entries := range [][]sprint.PinnedCard{{pin("two")}, {pin("two"), pin("one")}} {
		_, _, why := reviewedPromotionPins(s, "stream", promotionReview{Entries: entries}, []byte("exact review"))
		assert.Contains(t, why, "ordered queue prefix")
	}
	pins, replay, why := reviewedPromotionPins(s, "stream", promotionReview{Entries: []sprint.PinnedCard{pin("one"), pin("two")}}, []byte("exact review"))
	require.Empty(t, why)
	require.Len(t, pins, 2)
	assert.False(t, replay)
}

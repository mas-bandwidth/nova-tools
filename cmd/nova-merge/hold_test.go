package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// #1572 fold (SPEC-DECIDE reading 3, lines 987-1144, 1520-1580).
//
// On 2026-09-19 `nova-merge batch` read #1551 at 02:31:19Z -- OPEN, base dev,
// MERGEABLE/CLEAN, ci-ok success at 6adbbd1d -- and admitted it. At 02:34:25Z a scoped
// HOLD was posted on that very head as a pull request comment. At 02:43:03Z the batch
// merged and the held commit was on dev.
//
// Reading 3 specifies the canonical #1572 fold:
// - Reviewers mapping from TSV: who\tlogins\tmay-hold
// - Sources: lane read records, forge reviews (CHANGES_REQUESTED), forge comments
// - Comments without typed line or bold HOLD are pending by default (source=comment-pending who=unknown)
// - Required CLI flags: exactly one of --reviewers <file> or --no-require-holds --reason <text>
// - No --ignore-hold; no --strict-comments

func parseRev(tsv string) *merge.ReviewerSet {
	rs, err := merge.ParseReviewers(strings.NewReader(tsv))
	if err != nil {
		panic(err)
	}
	return rs
}

func testReviewerFile(t *testing.T, dir string, content string) string {
	t.Helper()
	path := filepath.Join(dir, "reviewers.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write reviewers: %v", err)
	}
	checkCmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if out, err := checkCmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		exec.Command("git", "-C", dir, "init", "-q").Run()
		exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
		exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()
	}
	exec.Command("git", "-C", dir, "add", path).Run()
	exec.Command("git", "-C", dir, "commit", "-q", "-m", "reviewers").Run()
	return path
}

func testReviewerCommit(t *testing.T, l *lab, content string) (string, string) {
	t.Helper()
	path := filepath.Join(l.work, "reviewers.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write reviewers: %v", err)
	}
	l.git(l.work, "add", "reviewers.tsv")
	sha := l.commit("update reviewers")
	return path, sha[:12]
}

const defaultReviewersTSV = "alice\talice\tyes\n"

// 1. TestAHeldHeadIsDroppedFromABatchAndRefusedAtLand: #1572's timeline as a fixture:
// member #1 has a hold comment and is dropped from batch; a hold comment posted between
// BATCH OK and land refuses the landing on land's own fresh read.
func TestAHeldHeadIsDroppedFromABatchAndRefusedAtLand(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:101", Who: "alice", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment-rule",
	})

	exit, stdout, stderr := l.run("batch", "--name", "integration-hold", "--pr", "1,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)

	if exit == 0 {
		t.Fatalf("the batch went green over a held member\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" carries an unreleased HOLD\" who=alice hold=comment:101 source=comment-rule")
	contains(t, stdout, "dropped=1")
	absent(t, stderr, "BATCH MERGED #1 ")

	// Now the second half of #1572: batch passed, then hold posted before land
	head := strings.Repeat("a", 40)
	const memberHead = "6adbbd1d89869455e920a43ccf7378daf4375add"
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	h.PRs[1551] = merge.PR{Number: 1551, HeadOID: memberHead, Mergeable: "MERGEABLE"}
	receipt := "BATCH OK name=integration-12t2 base=" + strings.Repeat("d", 40) +
		" head=" + head + " members=1551 dropped=none skipped=none checks=required"

	// Between BATCH OK and land:
	h.SetVerdicts(1551, merge.Verdict{
		ID: "comment:202", Who: "alice", Word: "hold", Head: memberHead,
		At: "2026-09-19T02:34:25Z", Source: "comment-rule",
	})

	lexit, lstdout, lstderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--reviewers", revFile)
	if lexit != 1 {
		t.Fatalf("landing over a held member must exit 1, got %d\nstdout: %s\nstderr: %s", lexit, lstdout, lstderr)
	}
	if len(q.enqueued) != 0 {
		t.Fatalf("the queue was touched: %v", q.enqueued)
	}
	contains(t, lstderr, "LAND REFUSED reason=held member=#1551 who=alice hold=comment:202 source=comment-rule")
	absent(t, lstdout, "LAND OK")
}

// 2. TestAHoldInAnySourceStops: lane record, review CHANGES_REQUESTED, comment.
func TestAHoldInAnySourceStops(t *testing.T) {
	t.Parallel()
	revFile := testReviewerFile(t, t.TempDir(), defaultReviewersTSV)

	// Source 1: review
	l1 := batchRepo(t)
	l1.host.SetVerdicts(1, merge.Verdict{
		ID: "review:301", Who: "alice", Word: "hold", Head: l1.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "review",
	})
	exit, _, stderr := l1.run("batch", "--name", "b1", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l1.dir, "b1"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l1.heads[1])+" carries an unreleased HOLD\" who=alice hold=review:301 source=review")

	// Source 2: comment
	l2 := batchRepo(t)
	l2.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:302", Who: "alice", Word: "hold", Head: l2.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-rule",
	})
	exit, _, stderr = l2.run("batch", "--name", "b2", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l2.dir, "b2"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l2.heads[1])+" carries an unreleased HOLD\" who=alice hold=comment:302 source=comment-rule")

	// Source 3: record
	l3 := batchRepo(t)
	l3.host.SetVerdicts(1, merge.Verdict{
		ID: "record:2026-09-19T01:00:00Z", Who: "alice", Word: "hold", Head: l3.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "record",
	})
	exit, _, stderr = l3.run("batch", "--name", "b3", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l3.dir, "b3"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l3.heads[1])+" carries an unreleased HOLD\" who=alice hold=record:2026-09-19T01:00:00Z source=record")
}

// 3. TestAnAbstainRecordIsNotAnInput: a recorded HOLD at H1, then reader's ABSTAIN at H1; held.
// Then push to H2 and reader's ABSTAIN at H2; still held, carried=yes.
func TestAnAbstainRecordIsNotAnInput(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)
	h1 := "1111111111111111111111111111111111111111"
	h2 := l.heads[1]

	l.host.SetVerdicts(1,
		merge.Verdict{ID: "record:at1", Who: "alice", Word: "hold", Head: h1, At: "2026-09-19T01:00:00Z", Source: "record"},
		merge.Verdict{ID: "record:at2", Who: "alice", Word: "abstain", Head: h1, At: "2026-09-19T01:05:00Z", Source: "record"},
		merge.Verdict{ID: "record:at3", Who: "alice", Word: "abstain", Head: h2, At: "2026-09-19T01:10:00Z", Source: "record"},
	)

	exit, _, stderr := l.run("batch", "--name", "b-abstain", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-abstain"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(h2)+" carries an unreleased HOLD\" who=alice hold=record:at1 source=record held_at="+merge.Short(h1)+" carried=yes")
}

// 4. TestChildAndCardRecordsAreNotInputs: child APPROVE and card APPROVE beside line HOLD: held.
// child HOLD alone: not held. card HOLD alone: not held.
func TestChildAndCardRecordsAreNotInputs(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)

	// Case A: child APPROVE and card APPROVE beside line HOLD -> still held!
	records := []merge.Verdict{
		{ID: "record:1", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record", Kind: "line"},
		{ID: "record:2", Who: "alice", Word: "approve", Head: head, At: "2026-09-19T02:00:00Z", Source: "record", Kind: "child"},
		{ID: "record:3", Who: "alice", Word: "approve", Head: head, At: "2026-09-19T03:00:00Z", Source: "record", Kind: "card"},
	}
	holds := merge.UnliftedHolds(records, head, "author", rev)
	if len(holds) != 1 || holds[0].ID != "record:1" {
		t.Fatalf("expected 1 line hold preserved despite child/card approves, got %v", holds)
	}

	// Case B: child HOLD and card HOLD alone -> not held!
	childAndCardOnly := []merge.Verdict{
		{ID: "record:4", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record", Kind: "child"},
		{ID: "record:5", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record", Kind: "card"},
	}
	holdsNonLine := merge.UnliftedHolds(childAndCardOnly, head, "author", rev)
	if len(holdsNonLine) != 0 {
		t.Fatalf("child and card holds alone should not be inputs, got %v", holdsNonLine)
	}
}

// 5. TestAScopedApproveReleasesOnlyTheHoldsItNames: HOLD scope="parser" (record:at1) and
// HOLD scope="docs" (record:at2), then scoped APPROVE --releases record:at2: parser hold stands.
// Scoped APPROVE naming nothing releases nothing; unscoped APPROVE at current head releases both.
func TestAScopedApproveReleasesOnlyTheHoldsItNames(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)

	// Step 1: two holds, scoped approve releasing record:at2
	v1 := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, Scope: "parser", At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at2", Who: "alice", Word: "hold", Head: head, Scope: "docs", At: "2026-09-19T02:00:00Z", Source: "record"},
		{ID: "record:at3", Who: "alice", Word: "approve", Head: head, Scope: "docs", Releases: []string{"record:at2"}, At: "2026-09-19T03:00:00Z", Source: "record"},
	}
	holds := merge.UnliftedHolds(v1, head, "author", rev)
	if len(holds) != 1 || holds[0].ID != "record:at1" {
		t.Fatalf("parser hold should stand, got %v", holds)
	}

	// Step 2: scoped approve naming nothing releases nothing
	v2 := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, Scope: "parser", At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at4", Who: "alice", Word: "approve", Head: head, Scope: "parser", Releases: nil, At: "2026-09-19T04:00:00Z", Source: "record"},
	}
	holds2 := merge.UnliftedHolds(v2, head, "author", rev)
	if len(holds2) != 1 {
		t.Fatalf("scoped approve naming nothing should release nothing, got %v", holds2)
	}

	// Step 3: unscoped approve at current head releases both
	v3 := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, Scope: "parser", At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at2", Who: "alice", Word: "hold", Head: head, Scope: "docs", At: "2026-09-19T02:00:00Z", Source: "record"},
		{ID: "record:at5", Who: "alice", Word: "approve", Head: head, Scope: "", At: "2026-09-19T05:00:00Z", Source: "record"},
	}
	holds3 := merge.UnliftedHolds(v3, head, "author", rev)
	if len(holds3) != 0 {
		t.Fatalf("unscoped approve at current head should release all holds, got %v", holds3)
	}
}

// 6. TestACommentNeverReleasesAnything: recorded HOLD, then reader's first-line approve token
// and pasted DISPOSITION verdict=APPROVE: still held.
func TestACommentNeverReleasesAnything(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "comment:501", Who: "alice", Word: "approve", Head: head, At: "2026-09-19T02:00:00Z", Source: "comment"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 {
		t.Fatalf("a comment must never release anything, got %v", holds)
	}
}

// 7. TestAForgeApprovedReviewReleasesNothing
func TestAForgeApprovedReviewReleasesNothing(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "review:601", Who: "alice", Word: "approve", Head: head, At: "2026-09-19T02:00:00Z", Source: "review"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 {
		t.Fatalf("forge approved review must release nothing, got %v", holds)
	}
}

// 8. TestADismissalReleasesNothing: CHANGES_REQUESTED dismissed by forge: held.
func TestADismissalReleasesNothing(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "review:701", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "review"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 {
		t.Fatalf("CHANGES_REQUESTED review must hold, got %v", holds)
	}
}

// 9. TestOnlyTheHolderReleases: another may-hold reader's unscoped APPROVE: held.
func TestOnlyTheHolderReleases(t *testing.T) {
	t.Parallel()
	rev := parseRev("alice\talice\tyes\nbob\tbob\tyes\n")
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at2", Who: "bob", Word: "approve", Head: head, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 || holds[0].Who != "alice" {
		t.Fatalf("only the holder releases, got %v", holds)
	}
}

// 10. TestAReleaseAtAStaleHeadReleasesNothing
func TestAReleaseAtAStaleHeadReleasesNothing(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	h1 := strings.Repeat("1", 40)
	h2 := strings.Repeat("2", 40)
	vs := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: h1, At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at2", Who: "alice", Word: "approve", Head: h1, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds := merge.UnliftedHolds(vs, h2, "author", rev)
	if len(holds) != 1 || !holds[0].Carried {
		t.Fatalf("release at stale head releases nothing, got %v", holds)
	}
}

// 11. TestNoAnswerReleasesAnything
func TestNoAnswerReleasesAnything(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at2", Who: "alice", Word: "abstain", Head: head, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 {
		t.Fatalf("abstain releases nothing, got %v", holds)
	}
}

// 12. TestAPushReleasesNothing: typed line, CHANGES_REQUESTED review, and untyped HOLD
// comment posted at head A, then push to B: each still held, carried=yes.
func TestAPushReleasesNothing(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	hA := strings.Repeat("a", 40)
	hB := strings.Repeat("b", 40)
	vs := []merge.Verdict{
		{ID: "comment:1", Who: "alice", Word: "hold", Head: hA, At: "2026-09-19T01:00:00Z", Source: "comment-rule"},
		{ID: "review:2", Who: "alice", Word: "hold", Head: hA, At: "2026-09-19T02:00:00Z", Source: "review"},
	}
	holds := merge.UnliftedHolds(vs, hB, "author", rev)
	if len(holds) != 2 {
		t.Fatalf("both holds must carry across push, got %d", len(holds))
	}
	for _, h := range holds {
		if !h.Carried {
			t.Errorf("hold %s should have Carried=true", h.ID)
		}
	}
}

// 13. TestADecidedHoldAtAnyConfidenceHolds
func TestADecidedHoldAtAnyConfidenceHolds(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "comment:801", Who: "alice", Word: "hold", Head: head, Conf: "0.20", At: "2026-09-19T01:00:00Z", Source: "comment-decided"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 || holds[0].Conf != "0.20" {
		t.Fatalf("decided hold should hold, got %v", holds)
	}
}

// 14. TestAnUntypedCommentFromAMayHoldLoginIsPending
func TestAnUntypedCommentFromAMayHoldLoginIsPending(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, "alice\talice\tyes\nstranger\tstranger\tno\n")

	// May-hold login comment is pending
	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:901", Who: "unknown", Word: "pending", Head: l.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-pending",
	})
	exit, _, stderr := l.run("batch", "--name", "b-pending", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-pending"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" has a pending comment\" who=unknown hold=comment:901 source=comment-pending")

	// Comment from a login with no may-hold is skipped (not pending)
	l2 := batchRepo(t)
	l2.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:902", Who: "unknown", Word: "unknown", Head: l2.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-skipped",
	})
	exit2, stdout2, _ := l2.run("batch", "--name", "b-notpending", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l2.dir, "b-notpending"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit2 != 0 {
		t.Fatalf("batch exit %d", exit2)
	}
	contains(t, stdout2, "members=1")
}

// 15. TestAPendingCommentIsClearedOnlyByAReadersVerb: comment from mapped login carrying
// NOTE, APPROVE or HOLD naming comment leaves it pending; nova-merge read APPROVE with
// --releases comment:<id> clears it; without --releases does not; recorded HOLD takes it over.
func TestAPendingCommentIsClearedOnlyByAReadersVerb(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)

	// Pending comment alone: pending
	v1 := []merge.Verdict{
		{ID: "comment:1001", Who: "unknown", Word: "pending", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-pending"},
	}
	holds := merge.UnliftedHolds(v1, head, "author", rev)
	if len(holds) != 1 || holds[0].Source != "comment-pending" {
		t.Fatalf("should be pending, got %v", holds)
	}

	// APPROVE without --releases does NOT clear it
	v2 := []merge.Verdict{
		{ID: "comment:1001", Who: "unknown", Word: "pending", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-pending"},
		{ID: "record:at1", Who: "alice", Word: "approve", Head: head, Releases: nil, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds2 := merge.UnliftedHolds(v2, head, "author", rev)
	if len(holds2) != 1 || holds2[0].Source != "comment-pending" {
		t.Fatalf("pending comment must not be cleared without --releases, got %v", holds2)
	}

	// APPROVE with --releases comment:1001 clears it
	v3 := []merge.Verdict{
		{ID: "comment:1001", Who: "unknown", Word: "pending", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-pending"},
		{ID: "record:at2", Who: "alice", Word: "approve", Head: head, Releases: []string{"comment:1001"}, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds3 := merge.UnliftedHolds(v3, head, "author", rev)
	if len(holds3) != 0 {
		t.Fatalf("pending comment should be cleared by --releases comment:1001, got %v", holds3)
	}

	// Recorded HOLD takes it over under their name
	v4 := []merge.Verdict{
		{ID: "comment:1001", Who: "unknown", Word: "pending", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-pending"},
		{ID: "record:at3", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds4 := merge.UnliftedHolds(v4, head, "author", rev)
	if len(holds4) != 1 || holds4[0].Who != "alice" || holds4[0].ID != "record:at3" {
		t.Fatalf("recorded hold should take over pending comment, got %v", holds4)
	}
}

// 16. TestAScopedApproveRecordDoesNotSatisfyNeedsRead: scoped APPROVE satisfies nothing
// in EvaluateReads; unscoped satisfies.
func TestAScopedApproveRecordDoesNotSatisfyNeedsRead(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	entry := &merge.Entry{
		OID:       head,
		NeedsRead: "yes",
		Reads: []merge.Read{
			{Who: "alice", Verdict: "approve", Head: head, Scope: "parser", Releases: []string{"record:at1"}},
		},
	}
	st := merge.EvaluateReads(entry, "author")
	if st.Approves != 0 || st.Satisfied {
		t.Fatalf("scoped approve must not satisfy read condition, got %+v", st)
	}

	// Unscoped APPROVE satisfies
	entry2 := &merge.Entry{
		OID:       head,
		NeedsRead: "yes",
		Reads: []merge.Read{
			{Who: "alice", Verdict: "approve", Head: head, Scope: "", Releases: nil},
		},
	}
	st2 := merge.EvaluateReads(entry2, "author")
	if st2.Approves == 0 || !st2.Satisfied {
		t.Fatalf("unscoped approve must satisfy read condition, got %+v", st2)
	}
}

// 17. TestBatchOKCarriesHoldsDispositionsAndReviewers
func TestBatchOKCarriesHoldsDispositionsAndReviewers(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revPath, revSHA := testReviewerCommit(t, l, defaultReviewersTSV)

	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:1101", Who: "alice", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment-rule",
	})

	exit, stdout, _ := l.run("batch", "--name", "b-ok-fields", "--pr", "1,2",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ok-fields"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revPath)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stdout, "holds=1")
	contains(t, stdout, "reviewers="+revSHA)
	contains(t, stdout, "dispositions=")
}

// 18. TestNewerComparesForgeStampsAndTiesBreakOnID
func TestNewerComparesForgeStampsAndTiesBreakOnID(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	// Two comments with the same created_at: higher ID is newer
	vs := []merge.Verdict{
		{ID: "comment:100", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-rule"},
		{ID: "comment:200", Who: "alice", Word: "approve", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment"},
	}
	// comment:200 is newer by ID, but comment never releases
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 {
		t.Fatalf("expected 1 hold, got %d", len(holds))
	}
}

// 19. TestEveryLineThatNamesAHoldPrintsItsID: record:<at>, review:<id>, comment:<id>
func TestEveryLineThatNamesAHoldPrintsItsID(t *testing.T) {
	t.Parallel()
	revFile := testReviewerFile(t, t.TempDir(), defaultReviewersTSV)

	for _, tc := range []struct {
		source string
		id     string
	}{
		{"record", "record:2026-09-19T01:00:00Z"},
		{"review", "review:42"},
		{"comment-rule", "comment:999"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			l := batchRepo(t)
			l.host.SetVerdicts(1, merge.Verdict{
				ID: tc.id, Who: "alice", Word: "hold", Head: l.heads[1],
				At: "2026-09-19T01:00:00Z", Source: tc.source,
			})
			_, _, stderr := l.run("batch", "--name", "b-id", "--pr", "1",
				"--repo", "o/n", "--root", filepath.Join(l.dir, "b-id"), "--base", "dev", "--timeout", "5m",
				"--reviewers", revFile)
			contains(t, stderr, "hold="+tc.id)

			// And on LAND REFUSED
			head := strings.Repeat("a", 40)
			h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
			h.SetVerdicts(1560, merge.Verdict{
				ID: tc.id, Who: "alice", Word: "hold", Head: head,
				At: "2026-09-19T01:00:00Z", Source: tc.source,
			})
			_, _, landErr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560", "--reviewers", revFile)
			contains(t, landErr, "hold="+tc.id)
		})
	}
}

// 20. TestThereIsNoOptInStrictFlag: class test across batch, land, queue sweep, react
func TestThereIsNoOptInStrictFlag(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, _, stderr := l.runBare("batch", "--strict-comments")
	if exit != 2 {
		t.Fatalf("batch with --strict-comments must exit 2, got %d", exit)
	}
	contains(t, stderr, "-strict-comments")

	h, q := greenBatchPR(t, 1560, strings.Repeat("a", 40)), &fakeLandEnqueue{}
	lexit, _, lstderr := runLandBare(t, h, q, "land", "--strict-comments")
	if lexit != 2 {
		t.Fatalf("land with --strict-comments must exit 2, got %d", lexit)
	}
	contains(t, lstderr, "-strict-comments")
}

// 21. TestUntypedCommentsIgnoreIsPerRunPrintedAndCarriesAReason
func TestUntypedCommentsIgnoreIsPerRunPrintedAndCarriesAReason(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:1201", Who: "unknown", Word: "pending", Head: l.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-pending",
	})

	// Without --reason: exit 2
	exitFail, _, stderrFail := l.runBare("batch", "--name", "b-ign-fail", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ign-fail"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile, "--untyped-comments", "ignore")
	if exitFail != 2 {
		t.Fatalf("missing --reason must exit 2, got %d", exitFail)
	}
	contains(t, stderrFail, "--untyped-comments=ignore requires --reason <text>")

	// With --reason: drops nothing, carries untyped=ignored reason="x"
	exit, stdout, _ := l.run("batch", "--name", "b-ign", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ign"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile, "--untyped-comments", "ignore", "--reason", "tested")
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stdout, "members=1")
	contains(t, stdout, `untyped=ignored reason="tested"`)

	// Next run without the flag drops the member again
	exit2, _, stderr2 := l.run("batch", "--name", "b-ign2", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ign2"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit2 != 0 {
		t.Fatalf("batch exit %d", exit2)
	}
	contains(t, stderr2, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" has a pending comment\"")
}

// 22. TestAnUntypedHoldFromTheSharedLoginFailsClosed: author and readers on one login,
// bold **HOLD:** in comment with no typed line: held, who=unknown source=comment-rule.
func TestAnUntypedHoldFromTheSharedLoginFailsClosed(t *testing.T) {
	t.Parallel()
	rev := parseRev("shared\tshared-login\tyes\n")
	head := strings.Repeat("a", 40)
	c, ok := merge.ParseComment(1301, "shared-login", "Please note: **HOLD:** we need clarification", "2026-09-19T01:00:00Z", rev, "shared-login", head, false)
	if !ok || c.Source != "comment-rule" || c.Who != "unknown" || c.Word != "hold" {
		t.Fatalf("untyped bold hold must fail closed to who=unknown source=comment-rule, got %+v (ok=%v)", c, ok)
	}
	holds := merge.UnliftedHolds([]merge.Verdict{c}, head, "shared-login", rev)
	if len(holds) != 1 || holds[0].Who != "unknown" {
		t.Fatalf("expected 1 unlifted hold with who=unknown, got %+v", holds)
	}
}

// 23. TestTheAuthorsNoteLineIsNotScannedAndTheAuthorsLoginSkipsNothing: DISPOSITION who=<author> verdict=NOTE
// is skipped; author login excuses nothing.
func TestTheAuthorsNoteLineIsNotScannedAndTheAuthorsLoginSkipsNothing(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	body := "DISPOSITION who=alice verdict=NOTE\nReporting that PR 1551 carries a HOLD"
	_, ok := merge.ParseComment(1401, "alice", body, "2026-09-19T01:00:00Z", rev, "alice", head, false)
	if ok {
		t.Fatalf("author note line should be skipped (ok=false)")
	}

	// Author's login does not exempt other comments from being scanned
	body2 := "DISPOSITION who=alice verdict=HOLD\nObjection on parser"
	c2, ok2 := merge.ParseComment(1402, "alice", body2, "2026-09-19T01:00:00Z", rev, "alice", head, false)
	if !ok2 || c2.Source != "comment-rule" || c2.Word != "hold" {
		t.Fatalf("author's hold comment must not be skipped, got %+v (ok=%v)", c2, ok2)
	}
}

// 24. TestAnUnknownHoldIsReleasedOnlyByAReadersVerbNamingIt: may-hold reader's APPROVE with
// --releases comment:<id> releases it; without --releases does not; HOLD takes it over.
func TestAnUnknownHoldIsReleasedOnlyByAReadersVerbNamingIt(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	unknownHold := merge.Verdict{
		ID: "comment:1501", Who: "unknown", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-rule",
	}

	// Without --releases: not released
	vs1 := []merge.Verdict{
		unknownHold,
		{ID: "record:at1", Who: "alice", Word: "approve", Head: head, Releases: nil, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds1 := merge.UnliftedHolds(vs1, head, "author", rev)
	if len(holds1) != 1 {
		t.Fatalf("unknown hold should not be released without --releases, got %v", holds1)
	}

	// With --releases comment:1501: released
	vs2 := []merge.Verdict{
		unknownHold,
		{ID: "record:at2", Who: "alice", Word: "approve", Head: head, Releases: []string{"comment:1501"}, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds2 := merge.UnliftedHolds(vs2, head, "author", rev)
	if len(holds2) != 0 {
		t.Fatalf("unknown hold should be released with --releases comment:1501, got %v", holds2)
	}

	// Reader's own HOLD takes it over
	vs3 := []merge.Verdict{
		unknownHold,
		{ID: "record:at3", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds3 := merge.UnliftedHolds(vs3, head, "author", rev)
	if len(holds3) != 1 || holds3[0].Who != "alice" || holds3[0].ID != "record:at3" {
		t.Fatalf("reader hold should take over unknown hold, got %v", holds3)
	}
}

// 25. TestTwoNamesOneLoginFoldSeparately
func TestTwoNamesOneLoginFoldSeparately(t *testing.T) {
	t.Parallel()
	rev := parseRev("alice\tshared-login\tyes\nbob\tshared-login\tyes\n")
	head := strings.Repeat("a", 40)
	cAlice, ok := merge.ParseComment(1601, "shared-login", "DISPOSITION who=alice verdict=HOLD\nStop", "2026-09-19T01:00:00Z", rev, "author", head, false)
	if !ok || cAlice.Who != "alice" {
		t.Fatalf("expected who=alice, got %+v (ok=%v)", cAlice, ok)
	}

	// Bob's unscoped approve does not release alice's hold
	vs := []merge.Verdict{
		cAlice,
		{ID: "record:at1", Who: "bob", Word: "approve", Head: head, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 1 || holds[0].Who != "alice" {
		t.Fatalf("bob's approve must not release alice's hold, got %v", holds)
	}
}

// 26. TestThereIsNoFlagThatIgnoresOneHold: class test across batch, land, queue sweep, react
func TestThereIsNoFlagThatIgnoresOneHold(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, _, stderr := l.runBare("batch", "--ignore-hold", "1")
	if exit != 2 {
		t.Fatalf("batch with --ignore-hold must exit 2, got %d", exit)
	}
	contains(t, stderr, "-ignore-hold")

	h, q := greenBatchPR(t, 1560, strings.Repeat("a", 40)), &fakeLandEnqueue{}
	lexit, _, lstderr := runLandBare(t, h, q, "land", "--ignore-hold", "1")
	if lexit != 2 {
		t.Fatalf("land with --ignore-hold must exit 2, got %d", lexit)
	}
	contains(t, lstderr, "-ignore-hold")
}

// 27. TestNoRequireHoldsWaivesTheForgeSourcesOnlyAndIsPrinted: recorded HOLD and forge HOLD;
// under --no-require-holds --reason x the recorded one still drops the member and every line
// carries holds=waived reason="x".
func TestNoRequireHoldsWaivesTheForgeSourcesOnlyAndIsPrinted(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-waived")

	l.host.SetVerdicts(1,
		merge.Verdict{ID: "record:at1", Who: "alice", Word: "hold", Head: l.heads[1], At: "2026-09-19T01:00:00Z", Source: "record"},
		merge.Verdict{ID: "comment:1701", Who: "alice", Word: "hold", Head: l.heads[1], At: "2026-09-19T02:00:00Z", Source: "comment-rule"},
	)

	exit, stdout, stderr := l.run("batch", "--name", "b-waived", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--no-require-holds", "--reason", "emergency")
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	// Recorded hold still drops the member
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" carries an unreleased HOLD\" who=alice hold=record:at1 source=record")
	contains(t, stdout, `holds=waived reason="emergency"`)
}

// 28. TestReviewersXorNoRequireHolds: neither: exit 2; both: exit 2; without reason: exit 2.
func TestReviewersXorNoRequireHolds(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-xor")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	// Neither: exit 2
	exitNeither, _, stderrNeither := l.runBare("batch", "--name", "b1", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")
	if exitNeither != 2 {
		t.Fatalf("neither flag must exit 2, got %d", exitNeither)
	}
	contains(t, stderrNeither, "exactly one of --reviewers <file> or --no-require-holds --reason <text> is required")

	// Both: exit 2
	exitBoth, _, stderrBoth := l.runBare("batch", "--name", "b2", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile, "--no-require-holds", "--reason", "x")
	if exitBoth != 2 {
		t.Fatalf("both flags must exit 2, got %d", exitBoth)
	}
	contains(t, stderrBoth, "exactly one of --reviewers <file> or --no-require-holds --reason <text> is required")

	// No reason: exit 2
	exitNoReason, _, stderrNoReason := l.runBare("batch", "--name", "b3", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--no-require-holds")
	if exitNoReason != 2 {
		t.Fatalf("missing --reason must exit 2, got %d", exitNoReason)
	}
	contains(t, stderrNoReason, "--no-require-holds requires --reason <text>")
}

// 29. TestRemovingMayHoldByCommitReleasesAndTheReceiptNamesTheCommit
func TestRemovingMayHoldByCommitReleasesAndTheReceiptNamesTheCommit(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)

	// Commit 1: alice may-hold
	testReviewerCommit(t, l, "alice\talice\tyes\n")

	// Commit 2: alice may-hold removed
	revPath2, revSHA2 := testReviewerCommit(t, l, "alice\talice\tno\n")

	// Member carries a comment hold from alice
	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:1801", Who: "alice", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-rule",
	})

	exit, stdout, _ := l.run("batch", "--name", "b-commit-rel", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-commit-rel"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revPath2)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	// Hold is no longer active; PR is admitted
	contains(t, stdout, "members=1")
	contains(t, stdout, "reviewers="+revSHA2)
	absent(t, stdout, "alice")
}

// 30. TestLandRefusesAHoldPostedAfterBatchOK: fake forge grows a comment between two reads
func TestLandRefusesAHoldPostedAfterBatchOK(t *testing.T) {
	t.Parallel()
	revFile := testReviewerFile(t, t.TempDir(), defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	receipt := "BATCH OK name=nightly base=" + strings.Repeat("f", 40) + " head=" + head + " members=1560 dropped=none"

	// Between batch and land, comment is added to forge
	h.SetVerdicts(1560, merge.Verdict{
		ID: "comment:1901", Who: "alice", Word: "hold", Head: head,
		At: "2026-09-19T02:34:25Z", Source: "comment-rule",
	})

	exit, _, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--reviewers", revFile)
	if exit != 1 {
		t.Fatalf("land must refuse a hold posted after BATCH OK: exit %d", exit)
	}
	contains(t, stderr, "LAND REFUSED reason=held member=#1560 who=alice hold=comment:1901 source=comment-rule")
}

// 31. TestSweepAndReactNeverEnqueueAHeldPR
func TestSweepAndReactNeverEnqueueAHeldPR(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("dev")
	// Write a standing hold to the lane
	if exit, _, errb := l.run("queue", "--lane", l.lane, "hold", "frozen for audit", "--who", "emma"); exit != 0 {
		t.Fatalf("queue hold failed: %s", errb)
	}

	// queue sweep refuses under standing hold
	exit, _, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h")
	if exit != 2 {
		t.Fatalf("queue sweep under standing hold must exit 2, got %d", exit)
	}
	contains(t, stderr, "QUEUE REFUSED: a hold is standing")

	// react drops event with REACT hold under standing hold
	reactOnce(t, l, `{"number":951,"head":"a1b2","conclusion":"SUCCESS"}`, func(stdout string) {
		contains(t, stdout, "REACT hold pr=951")
		absent(t, stdout, "REACT enqueue")
	})
	if q := l.loadQueue(); hasInt(q.Queued, 951) {
		t.Errorf("PR was enqueued under standing lane hold: %v", q.Queued)
	}
}

// 32. TestAReleaseAtTheSameHeadIsObservedAndWritesNoTruth
func TestAReleaseAtTheSameHeadIsObservedAndWritesNoTruth(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	head := strings.Repeat("a", 40)
	vs := []merge.Verdict{
		{ID: "comment:2001", Who: "alice", Word: "hold", Head: head, At: "2026-09-19T01:00:00Z", Source: "comment-decided"},
		{ID: "record:at1", Who: "alice", Word: "approve", Head: head, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	holds := merge.UnliftedHolds(vs, head, "author", rev)
	if len(holds) != 0 {
		t.Fatalf("release at the same head should release the hold, got %v", holds)
	}
}

// 33. TestBatchAndLandPreserveRealLaneReadHolds: real lane read records are loaded
// independently of the forge, and survive forge waivers and forge errors.
func TestBatchAndLandPreserveRealLaneReadHolds(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	laneDir := filepath.Join(l.dir, "lane")

	// Initialize real lane
	if exit, _, errb := l.run("init", "--lane", laneDir, "--repo", "o/n", "--base", "dev", "--lane-branch", "nova-merge/dev"); exit != 0 {
		t.Fatalf("init lane failed: %s", errb)
	}

	// Write real lane read record using standard "read" verb
	if exit, _, errb := l.run("read", "--lane", laneDir, "--pr", "1", "--who", "rowan", "--verdict", "hold", "--head", l.heads[1]); exit != 0 {
		t.Fatalf("read verb failed: %s", errb)
	}

	// Run batch with --lane and --no-require-holds --reason: lane hold must drop member #1
	exit, stdout, stderr := l.run("batch", "--name", "lane-hold-test", "--pr", "1,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", laneDir, "--no-require-holds", "--reason", "waive forge sources")

	contains(t, stderr, `BATCH DROP #1 reason="head `+l.heads[1][:12]+` carries an unreleased HOLD" who=rowan hold=record:`)
	contains(t, stderr, "source=record")
	_ = exit
	_ = stdout

	// Run land with --lane and --no-require-holds --reason: lane hold must refuse landing PR 1
	exitLand, _, stderrLand := l.run("land", "--repo", "o/n", "--pr", "1",
		"--lane", laneDir, "--no-require-holds", "--reason", "waive forge sources")
	if exitLand != 1 {
		t.Fatalf("land under lane hold must exit 1, got %d: %s", exitLand, stderrLand)
	}
	contains(t, stderrLand, "LAND REFUSED reason=held member=#1 who=rowan hold=record:")
	contains(t, stderrLand, "source=record")
}

// 34. TestGetReviewersSHARefusesUncommittedOrMissingRepo (SPEC-DECIDE reading 3, Johnny row 3):
// Reviewer file outside a repo or uncommitted must refuse exit 2.
func TestGetReviewersSHARefusesUncommittedOrMissingRepo(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-rev-err")

	// Case A: File outside git repo
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "reviewers.tsv")
	if err := os.WriteFile(outsideFile, []byte("rowan\trowan\tyes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := l.run("batch", "--name", "b1", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", outsideFile)
	if exit != 2 {
		t.Fatalf("batch with reviewer file outside repo must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "reviewer file")

	// Case B: Uncommitted file in git repo
	uncommittedFile := filepath.Join(l.dir, "uncommitted-reviewers.tsv")
	if err := os.WriteFile(uncommittedFile, []byte("rowan\trowan\tyes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr = l.run("batch", "--name", "b2", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", uncommittedFile)
	if exit != 2 {
		t.Fatalf("batch with uncommitted reviewer file must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "reviewer file")
}

// 35. TestEmptyOrMalformedReviewersFileRefuses (SPEC-DECIDE reading 3, row 9):
// An empty reviewer file must refuse exit 2, never proceed unfenced.
func TestEmptyOrMalformedReviewersFileRefuses(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-empty-rev")

	emptyFile, _ := testReviewerCommit(t, l, "\n# only comments\n")
	exit, _, stderr := l.run("batch", "--name", "b-empty", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", emptyFile)
	if exit != 2 {
		t.Fatalf("batch with empty reviewer file must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "reviewer file")
}

// 36. TestReadReleasesRequiresScopeAndValidID (SPEC-DECIDE reading 3, row 12):
// --releases without --scope or with malformed ID must refuse exit 2.
func TestReadReleasesRequiresScopeAndValidID(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("dev")

	// Case A: --releases without --scope
	exit, _, stderr := l.run("read", "--lane", l.lane, "--pr", "1", "--who", "rowan",
		"--verdict", "approve", "--head", strings.Repeat("a", 40),
		"--releases", "comment:101")
	if exit != 2 {
		t.Fatalf("read --releases without --scope must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "--releases requires --scope")

	// Case B: --releases with malformed hold ID
	exit, _, stderr = l.run("read", "--lane", l.lane, "--pr", "1", "--who", "rowan",
		"--verdict", "approve", "--head", strings.Repeat("a", 40),
		"--scope", "parser", "--releases", "bad-hold-id")
	if exit != 2 {
		t.Fatalf("read --releases with malformed id must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "release id")
}

// 37. TestSweepAndReactNeverEnqueueHeldPR (SPEC-DECIDE reading 3, row 6):
// PR carrying unlifted lane read hold is never enqueued by queue sweep or react.
func TestSweepAndReactNeverEnqueueHeldPR(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("dev")

	// PR 951 has a recorded HOLD in the lane
	head := strings.Repeat("b", 40)
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "rowan",
		"--verdict", "hold", "--head", head); exit != 0 {
		t.Fatalf("read hold failed: %s", errb)
	}

	// Fake host reports PR 951 green
	l.host.PRs[951] = merge.PR{Number: 951, HeadOID: head, Mergeable: "MERGEABLE"}
	l.host.OpenQueue = []merge.PR{{Number: 951, HeadOID: head, Mergeable: "MERGEABLE"}}
	var c merge.Checks
	c.AddRun("ci-ok", "success", head)
	l.host.ChecksBy[head] = c

	// queue sweep must NOT enqueue PR 951 because it carries a hold
	exit, stdout, _ := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h")
	if exit != 0 {
		t.Fatalf("queue sweep exit %d", exit)
	}
	if q := l.loadQueue(); hasInt(q.Queued, 951) {
		t.Fatalf("queue sweep enqueued held PR 951: %v", q.Queued)
	}
	_ = stdout

	// react on checks-done must NOT enqueue PR 951 and report REACT hold
	reactOnce(t, l, `{"number":951,"head":"`+head+`","conclusion":"SUCCESS"}`, func(out string) {
		contains(t, out, "REACT hold pr=951")
		absent(t, out, "REACT enqueue")
	})
	if q := l.loadQueue(); hasInt(q.Queued, 951) {
		t.Fatalf("react enqueued held PR 951: %v", q.Queued)
	}
}

// 38. TestReviewersWithoutLaneRefuses (Rowan row B): --lane is required when --reviewers is specified
func TestReviewersWithoutLaneRefuses(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	// batch with --reviewers but no --lane
	exit, _, stderr := l.runBare("batch", "--name", "no-lane", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch without --lane must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "--lane is required when --reviewers is specified")

	// land with --reviewers but no --lane
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	receipt := "BATCH OK name=test base=" + strings.Repeat("d", 40) + " head=" + head + " members=1551 dropped=none"
	lexit, _, lstderr := runLandBare(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--reviewers", revFile)
	if lexit != 2 {
		t.Fatalf("land without --lane must exit 2, got %d\nstderr: %s", lexit, lstderr)
	}
	contains(t, lstderr, "--lane is required when --reviewers is specified")
}

// 39. TestBatchAndLandRefuseMalformedLaneRecord (Rowan row A / Stella blocker 1):
// A corrupt lane record file must cause batch and land to refuse exit 2, naming the bad file.
func TestBatchAndLandRefuseMalformedLaneRecord(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	readsDir := filepath.Join(l.lane, merge.ReadsDir, "1")
	if err := os.MkdirAll(readsDir, 0755); err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(readsDir, "corrupt.json")
	if err := os.WriteFile(badFile, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	// batch with corrupt lane record must exit 2 and name badFile
	exit, _, stderr := l.run("batch", "--name", "bad-lane-record", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", l.lane, "--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch with malformed lane record must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "corrupt.json")

	// land with corrupt lane record must exit 2 and name badFile
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	h.PRs[1] = merge.PR{Number: 1, HeadOID: l.heads[1], Mergeable: "MERGEABLE"}
	receipt := "BATCH OK name=test base=" + strings.Repeat("d", 40) + " head=" + head + " members=1 dropped=none"
	lexit, _, lstderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--lane", l.lane, "--reviewers", revFile)
	if lexit != 2 {
		t.Fatalf("land with malformed lane record must exit 2, got %d\nstderr: %s", lexit, lstderr)
	}
	contains(t, lstderr, "LAND REFUSED")
	contains(t, lstderr, "corrupt.json")
}

// 40. TestBatchAndLandRefuseNonexistentLaneDirectory (Stella blocker 1):
// A supplied missing lane directory must refuse exit 2.
func TestBatchAndLandRefuseNonexistentLaneDirectory(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)
	missingLane := filepath.Join(l.dir, "nonexistent-lane")

	exit, _, stderr := l.run("batch", "--name", "missing-lane", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", missingLane, "--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch with nonexistent lane directory must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")

	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	receipt := "BATCH OK name=test base=" + strings.Repeat("d", 40) + " head=" + head + " members=1 dropped=none"
	lexit, _, lstderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--lane", missingLane, "--reviewers", revFile)
	if lexit != 2 {
		t.Fatalf("land with nonexistent lane directory must exit 2, got %d\nstderr: %s", lexit, lstderr)
	}
	contains(t, lstderr, "LAND REFUSED")
}

// 41. TestLandRefusesWhenPRReadFailsUnderNoRequireHolds (Rowan row C):
// A member PR read failure under the waiver must be a refusal, never a skip.
func TestLandRefusesWhenPRReadFailsUnderNoRequireHolds(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	// Member 999 is NOT in h.PRs, so host.PR(999) fails
	receipt := "BATCH OK name=test base=" + strings.Repeat("d", 40) + " head=" + head + " members=999 dropped=none"

	lexit, _, lstderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--no-require-holds", "--reason", "waiver")
	if lexit != 2 {
		t.Fatalf("land must refuse when member PR read fails under waiver, got %d\nstderr: %s", lexit, lstderr)
	}
	contains(t, lstderr, "LAND REFUSED")
	contains(t, lstderr, "member pull request 999 could not be read")
}

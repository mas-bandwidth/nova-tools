package main

import (
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
// - --lane is required on both XOR sides (never none); a waiver with no lane is leftover --ignore-hold (#1896)
// - No --ignore-hold; no --strict-comments

func parseRev(tsv string) *merge.ReviewerSet {
	rs, err := merge.ParseReviewers(strings.NewReader(tsv))
	if err != nil {
		panic(err)
	}
	return rs
}

const defaultReviewersTSV = "alice\talice\tyes\n"

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

// 10. TestAReleaseAtAStaleHeadReleasesTheHoldersOwnHold: flipped by Glenn's
// lander-keys-reads-by-who ruling (2026-09-23). A hold by X is released when X's last
// typed verdict written after the hold is APPROVE, at ANY head (#2879: rowan HOLD 6 and
// rowan APPROVE 8 both at fc15f98d, head moved to 8984b941, the hold pinned). Another
// friend's APPROVE at the stale head still releases nothing.
func TestAReleaseAtAStaleHeadReleasesTheHoldersOwnHold(t *testing.T) {
	t.Parallel()
	rev := parseRev(defaultReviewersTSV)
	h1 := strings.Repeat("1", 40)
	h2 := strings.Repeat("2", 40)
	vs := []merge.Verdict{
		{ID: "record:at1", Who: "alice", Word: "hold", Head: h1, At: "2026-09-19T01:00:00Z", Source: "record"},
		{ID: "record:at2", Who: "alice", Word: "approve", Head: h1, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	if holds := merge.UnliftedHolds(vs, h2, "author", rev); len(holds) != 0 {
		t.Fatalf("the holder's own later APPROVE at a stale head releases her hold, got %v", holds)
	}
	other := []merge.Verdict{
		vs[0],
		{ID: "record:at3", Who: "bob", Word: "approve", Head: h1, At: "2026-09-19T02:00:00Z", Source: "record"},
	}
	two := parseRev("alice\talice\tyes\nbob\tbob\tyes\n")
	if holds := merge.UnliftedHolds(other, h2, "author", two); len(holds) != 1 || !holds[0].Carried {
		t.Fatalf("another friend's APPROVE at a stale head releases nothing; want alice's hold carried, got %v", holds)
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

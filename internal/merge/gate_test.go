package merge

import (
	"strings"
	"testing"
)

// 1. EvaluateVerdicts strictly requires record provenance (v.Source == "record").
// Forge claims (comments or reviews) claiming APPROVE are never counted.
func TestEvaluateVerdicts_RequiresRecordProvenance(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	// Case A: Forge comments/reviews claiming APPROVE must NOT count as approvals
	forgeClaims := []Verdict{
		{ID: "comment:1", Who: "stella", Head: head, Word: "approve", At: "2026-09-19T10:00:00Z", Source: "comment-rule"},
		{ID: "review:2", Who: "johnny", Head: head, Word: "approve", At: "2026-09-19T10:05:00Z", Source: "review"},
	}
	stA := EvaluateVerdicts(forgeClaims, head, author, rs)
	if stA.Approves != 0 || len(stA.Approvers) != 0 {
		t.Fatalf("forge claims without record provenance must not be counted as approvals, got %+v", stA)
	}

	// Case B: Real lane records (Source == "record") count properly
	recordVerdicts := []Verdict{
		// Valid record approve from stella
		{ID: "record:1", Who: "stella", Head: head, Word: "approve", At: "2026-09-19T10:00:00Z", Source: "record"},
		// Duplicate record approve from stella
		{ID: "record:2", Who: "stella", Head: head, Word: "approve", At: "2026-09-19T10:05:00Z", Source: "record"},
		// Author rowan self-approval - MUST BE EXCLUDED
		{ID: "record:3", Who: "rowan", Head: head, Word: "approve", At: "2026-09-19T10:10:00Z", Source: "record"},
		// Unauthorized reviewer bot - MUST BE EXCLUDED
		{ID: "record:4", Who: "bot", Head: head, Word: "approve", At: "2026-09-19T10:15:00Z", Source: "record"},
		// Stale head approve - MUST BE EXCLUDED
		{ID: "record:5", Who: "johnny", Head: strings.Repeat("b", 40), Word: "approve", At: "2026-09-19T10:20:00Z", Source: "record"},
		// Scoped approve - MUST NOT count for unscoped gate
		{ID: "record:6", Who: "johnny", Head: head, Word: "approve", At: "2026-09-19T10:25:00Z", Scope: "parser", Source: "record"},
	}

	got := CountTypedApproves(recordVerdicts, head, author, rs)
	if got != 1 {
		t.Fatalf("expected exactly 1 approve (stella), got %d", got)
	}

	stB := EvaluateVerdicts(recordVerdicts, head, author, rs)
	if stB.Approves != 1 || len(stB.Approvers) != 1 || stB.Approvers[0] != "stella" {
		t.Fatalf("unexpected standing: %+v", stB)
	}
	if stB.Held || !stB.Satisfied {
		t.Fatalf("expected not held and satisfied, got %+v", stB)
	}
}

// 2. Active holders cannot approve.
func TestEvaluateVerdicts_ActiveHolderCannotApprove(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	verdicts := []Verdict{
		// Active hold by stella
		{ID: "comment:10", Who: "stella", Head: head, Word: "hold", At: "2026-09-19T10:00:00Z", Source: "comment-rule"},
		// stella also has a record approve (e.g. from earlier or invalid sequence)
		{ID: "record:11", Who: "stella", Head: head, Word: "approve", At: "2026-09-19T09:00:00Z", Source: "record"},
		// johnny has a clean record approve
		{ID: "record:12", Who: "johnny", Head: head, Word: "approve", At: "2026-09-19T10:30:00Z", Source: "record"},
	}

	st := EvaluateVerdicts(verdicts, head, author, rs)
	if !st.Held || st.Holds != 1 {
		t.Fatalf("expected 1 active hold, got %+v", st)
	}
	// stella is active holder, so only johnny's approval counts
	if st.Approves != 1 || len(st.Approvers) != 1 || st.Approvers[0] != "johnny" {
		t.Fatalf("expected 1 approval from johnny (excluding active holder stella), got %+v", st)
	}
	if st.Satisfied {
		t.Fatalf("gate must not be satisfied when held")
	}
}

// 3. Comment promotion is refused; missing head in ParseComment does not supply current head.
func TestCommentPromotionRefusedAndNoMissingHeadDefault(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)

	// Case A: Typed APPROVE in comment must NOT produce an approval verdict
	bodyApprove := "DISPOSITION who=stella head=" + head + " verdict=APPROVE\nLGTM"
	vApprove, okApprove := ParseComment(101, "stella-astra", bodyApprove, "2026-09-19T10:00:00Z", rs, "author", head, false)
	if okApprove && vApprove.Word == "approve" {
		t.Fatalf("comment must NOT be promoted to APPROVE, got %+v", vApprove)
	}

	// Case B: Typed HOLD without explicit head must NOT supply currentHead
	bodyHoldNoHead := "DISPOSITION who=stella verdict=HOLD scope=\"perf\"\nBlocking this change."
	vHold, okHold := ParseComment(102, "stella-astra", bodyHoldNoHead, "2026-09-19T10:00:00Z", rs, "author", head, false)
	if !okHold {
		t.Fatalf("expected typed HOLD to be parsed")
	}
	if vHold.Head == head {
		t.Fatalf("ParseComment must NOT supply currentHead when head is absent on typed disposition, got head=%q", vHold.Head)
	}
	if vHold.Head != "" {
		t.Fatalf("expected empty head on typed disposition without head, got %q", vHold.Head)
	}
}

// 4. Untyped comments and review bodies are ignored with ignoreUntyped=true.
func TestUntypedCommentsIgnoredWithFlag(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)

	untypedBody := "I looked over the docs changes, they seem reasonable."

	// Test ParseComment with ignoreUntyped=false -> pending hold
	vCommentFalse, okCommentFalse := ParseComment(201, "stella-astra", untypedBody, "2026-09-19T10:00:00Z", rs, "author", head, false)
	if !okCommentFalse || vCommentFalse.Word != "pending" || vCommentFalse.Source != "comment-pending" {
		t.Fatalf("untyped comment without ignoreUntyped must be pending, got %+v", vCommentFalse)
	}

	// Test ParseComment with ignoreUntyped=true -> ignored
	_, okCommentTrue := ParseComment(202, "stella-astra", untypedBody, "2026-09-19T10:00:00Z", rs, "author", head, true)
	if okCommentTrue {
		t.Fatalf("untyped comment with ignoreUntyped=true must be ignored")
	}

	// Test ParseReview (COMMENTED) with ignoreUntyped=false -> pending
	vReviewFalse, okReviewFalse := ParseReview(301, "stella-astra", untypedBody, "COMMENTED", head, "2026-09-19T10:00:00Z", rs, "author", head, false)
	if !okReviewFalse || vReviewFalse.Word != "pending" {
		t.Fatalf("COMMENTED review without ignoreUntyped must be pending, got %+v", vReviewFalse)
	}

	// Test ParseReview (COMMENTED) with ignoreUntyped=true -> ignored
	_, okReviewTrue := ParseReview(302, "stella-astra", untypedBody, "COMMENTED", head, "2026-09-19T10:00:00Z", rs, "author", head, true)
	if okReviewTrue {
		t.Fatalf("COMMENTED review with ignoreUntyped=true must be ignored")
	}
}

// 5. Distinct namespaces for finding IDs (comment vs review vs bare) are strictly partitioned.
func TestDistinctNamespacesStrictlyPartitioned(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	// Case A: Hold is comment:123
	holdComment := Verdict{
		ID:     "comment:123",
		Who:    "stella",
		Head:   head,
		Word:   "hold",
		At:     "2026-09-19T10:00:00Z",
		Source: "comment-rule",
	}

	// Record naming review:123 does NOT release comment:123
	readWrongPrefix := Read{
		Who:      "stella",
		Verdict:  "approve",
		Head:     head,
		At:       "2026-09-19T11:00:00Z",
		Scope:    "parser",
		Releases: []string{"review:123"},
	}
	remainingA := UnreleasedHolds([]Verdict{holdComment}, []Read{readWrongPrefix}, head, author, rs)
	if len(remainingA) != 1 || remainingA[0].ID != "comment:123" {
		t.Fatalf("review:123 must not release comment:123; remaining: %v", remainingA)
	}

	// Record naming bare 123 does NOT release comment:123
	readBare := Read{
		Who:      "stella",
		Verdict:  "approve",
		Head:     head,
		At:       "2026-09-19T11:00:00Z",
		Scope:    "parser",
		Releases: []string{"123"},
	}
	remainingB := UnreleasedHolds([]Verdict{holdComment}, []Read{readBare}, head, author, rs)
	if len(remainingB) != 1 || remainingB[0].ID != "comment:123" {
		t.Fatalf("bare 123 must not release comment:123; remaining: %v", remainingB)
	}

	// Record naming exact typed comment:123 DOES release it
	readExact := Read{
		Who:      "stella",
		Verdict:  "approve",
		Head:     head,
		At:       "2026-09-19T11:00:00Z",
		Scope:    "parser",
		Releases: []string{"comment:123"},
	}
	remainingC := UnreleasedHolds([]Verdict{holdComment}, []Read{readExact}, head, author, rs)
	if len(remainingC) != 0 {
		t.Fatalf("exact typed comment:123 must release hold, got %v", remainingC)
	}

	// Case B: Colliding numbers: comment:123 and review:123 both active from stella.
	// A release naming only comment:123 releases comment:123, but review:123 MUST REMAIN ACTIVE.
	holdReview := Verdict{
		ID:     "review:123",
		Who:    "stella",
		Head:   head,
		Word:   "hold",
		At:     "2026-09-19T10:05:00Z",
		Source: "review",
	}
	colliding := UnreleasedHolds([]Verdict{holdComment, holdReview}, []Read{readExact}, head, author, rs)
	if len(colliding) != 1 || colliding[0].ID != "review:123" {
		t.Fatalf("colliding numeric ID review:123 must not be released by comment:123; got: %v", colliding)
	}
}

// 6. When explicit release IDs are supplied, an unscoped or scoped approve does NOT
// release all of the author's holds; it only releases the explicitly named IDs.
func TestExplicitReleasesDoesNotReleaseAllHolds(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	holds := []Verdict{
		{ID: "comment:101", Who: "stella", Head: head, Word: "hold", At: "2026-09-19T10:00:00Z", Source: "comment-rule"},
		{ID: "comment:102", Who: "stella", Head: head, Word: "hold", At: "2026-09-19T10:05:00Z", Source: "comment-rule"},
	}

	// An approval record with Scope: "" (unscoped) but with explicit Releases: ["comment:101"]
	read := Read{
		Who:      "stella",
		Verdict:  "approve",
		Head:     head,
		At:       "2026-09-19T11:00:00Z",
		Scope:    "", // even if scope is empty
		Releases: []string{"comment:101"},
	}

	unlifted := UnreleasedHolds(holds, []Read{read}, head, author, rs)
	if len(unlifted) != 1 || unlifted[0].ID != "comment:102" {
		t.Fatalf("explicit release list must only release comment:101 and NOT comment:102; got: %v", unlifted)
	}
}

// 7. Typed or explicit first-line APPROVE stops HOLD heading/bold fall-through (#2454).
func TestTypedOrExplicitApproveStopsHoldFallthrough(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	// Case A: Typed DISPOSITION verdict=APPROVE with a # HOLD heading lower down
	bodyTypedApprove := "DISPOSITION who=stella head=" + head + " verdict=APPROVE\nLGTM\n### HOLD notes from yesterday\nSome notes."
	vTyped, okTyped := ParseComment(201, "stella-astra", bodyTypedApprove, "2026-09-19T10:00:00Z", rs, author, head, false)
	if okTyped {
		t.Fatalf("typed APPROVE must be inert and not produce a hold or pending verdict, got: %+v", vTyped)
	}

	// Case B: Explicit first-line APPROVE with a # HOLD heading lower down
	bodyExplicitApprove := "APPROVE at " + head + "\nLGTM\n### HOLD notes\nSome history."
	vExplicit, okExplicit := ParseComment(202, "stella-astra", bodyExplicitApprove, "2026-09-19T10:00:00Z", rs, author, head, true)
	if okExplicit {
		t.Fatalf("explicit first-line APPROVE with ignoreUntyped=true must be ignored and not produce a hold, got: %+v", vExplicit)
	}
}

// 8. TestACommentNeverReleasesAnything driven from ParseComment output.
func TestACommentNeverReleasesAnything_DrivenFromParseComment(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	// alice had recorded a hold in the lane
	hold := Verdict{
		ID:     "record:at1",
		Who:    "stella",
		Head:   head,
		Word:   "hold",
		At:     "2026-09-19T01:00:00Z",
		Source: "record",
	}

	// stella posts a comment claiming APPROVE
	commentBody := "DISPOSITION who=stella head=" + head + " verdict=APPROVE\nLGTM"
	vComment, okComment := ParseComment(301, "stella-astra", commentBody, "2026-09-19T02:00:00Z", rs, author, head, false)

	vs := []Verdict{hold}
	if okComment {
		vs = append(vs, vComment)
	}

	// The hold must still be unlifted
	unlifted := UnliftedHolds(vs, head, author, rs)
	if len(unlifted) != 1 || unlifted[0].ID != "record:at1" {
		t.Fatalf("a comment claiming approve must never release a hold, got: %v", unlifted)
	}
}

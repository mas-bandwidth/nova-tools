package merge

import (
	"strings"
	"testing"
)

func sampleReviewers() *ReviewerSet {
	tsv := `who	logins	may-hold
rowan	rowan-claude,claude	yes
stella	stella-astra,astra	yes
johnny	johnny-grok	yes
bot	ci-bot	no
`
	rs, _ := ParseReviewers(strings.NewReader(tsv))
	return rs
}

func TestStripQuotedAndCode(t *testing.T) {
	input := `Hello
> quoted line 1
> quoted line 2
Real text here
` + "```" + `
code block
HOLD in code
` + "```" + `
After code`

	got := StripQuotedAndCode(input)
	if strings.Contains(got, "quoted line") {
		t.Errorf("quoted lines were not stripped: %s", got)
	}
	if strings.Contains(got, "HOLD in code") {
		t.Errorf("code block was not stripped: %s", got)
	}
	if !strings.Contains(got, "Real text here") || !strings.Contains(got, "After code") {
		t.Errorf("real text was lost: %s", got)
	}
}

func TestParseDispositionLine(t *testing.T) {
	line := `DISPOSITION who=stella head=cb08e876640d3ab5086585b26f87067fac1f6ee8 verdict=HOLD scope="parser"`
	who, head, verdict, scope, ok := ParseDispositionLine(line)
	if !ok {
		t.Fatalf("failed to parse disposition line")
	}
	if who != "stella" || head != "cb08e876640d3ab5086585b26f87067fac1f6ee8" || verdict != "HOLD" || scope != "parser" {
		t.Fatalf("mismatched parse: who=%s head=%s verdict=%s scope=%s", who, head, verdict, scope)
	}
}

func TestAuthorNoteSkipped(t *testing.T) {
	body := "DISPOSITION who=rowan verdict=NOTE\nstatus report mentioning HOLD"
	if !IsAuthorNote(body, "rowan") {
		t.Errorf("expected author note to be recognized")
	}
	if IsAuthorNote(body, "stella") {
		t.Errorf("author note for rowan should not match author stella")
	}
}

func TestParseCommentHoldRule(t *testing.T) {
	rs := sampleReviewers()
	head := "1111111111111111111111111111111111111111"

	// 1. Typed DISPOSITION HOLD
	typed := "DISPOSITION who=stella head=1111111111111111111111111111111111111111 verdict=HOLD\nSome comment"
	v, ok := ParseComment(101, "astra", typed, "2026-09-19T10:00:00Z", rs, "author", head, false)
	if !ok || v.Word != "hold" || v.Who != "stella" || v.Source != "comment-rule" {
		t.Fatalf("typed comment failed: %+v, ok=%v", v, ok)
	}

	// 2. Bold **HOLD:** from shared login -> who=unknown
	bold := "**HOLD: live-owner exclusion is still lost.**"
	v, ok = ParseComment(102, "claude", bold, "2026-09-19T10:05:00Z", rs, "author", head, false)
	if !ok || v.Word != "hold" || v.Who != "unknown" || v.Source != "comment-rule" {
		t.Fatalf("bold hold failed: %+v, ok=%v", v, ok)
	}

	// 3. Untyped comment from may-hold login -> pending by default
	untyped := "Looks interesting, let me check CI"
	v, ok = ParseComment(103, "claude", untyped, "2026-09-19T10:10:00Z", rs, "author", head, false)
	if !ok || v.Word != "pending" || v.Who != "unknown" || v.Source != "comment-pending" {
		t.Fatalf("untyped comment should be pending: %+v, ok=%v", v, ok)
	}

	// 4. Untyped comment with ignoreUntyped=true -> ignored
	_, ok = ParseComment(104, "claude", untyped, "2026-09-19T10:15:00Z", rs, "author", head, true)
	if ok {
		t.Fatalf("untyped comment should be ignored when ignoreUntyped is true")
	}

	// 5. Untyped comment from login with may-hold=false (bot) -> not pending
	_, ok = ParseComment(105, "ci-bot", untyped, "2026-09-19T10:20:00Z", rs, "author", head, false)
	if ok {
		t.Fatalf("untyped comment from non-may-hold login should not be pending")
	}

	// 6. Comment from foreign login (not in reviewer file) -> foreign, not added
	v, ok = ParseComment(106, "stranger", "HOLD", "2026-09-19T10:25:00Z", rs, "author", head, false)
	if ok || !v.Foreign {
		t.Fatalf("stranger should be foreign and not added: %+v", v)
	}
}

func TestUnreleasedHoldsFold(t *testing.T) {
	rs := sampleReviewers()
	headA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Scenario 1: Unscoped APPROVE releases hold of same who at current head
	holds := []Verdict{
		{ID: "record:2026-09-19T01:00:00Z", Who: "stella", Word: "hold", Head: headA, At: "2026-09-19T01:00:00Z", Source: "record"},
	}
	reads := []Read{
		{Who: "stella", Verdict: "approve", Head: headA, At: "2026-09-19T02:00:00Z"},
	}
	unreleased := UnreleasedHolds(holds, reads, headA, "rowan", rs)
	if len(unreleased) != 0 {
		t.Fatalf("expected hold to be released, got: %+v", unreleased)
	}

	// Scenario 2: Scoped APPROVE releases ONLY named hold
	holds = []Verdict{
		{ID: "record:2026-09-19T01:00:00Z", Who: "stella", Word: "hold", Head: headA, At: "2026-09-19T01:00:00Z", Source: "record", Scope: "parser"},
		{ID: "record:2026-09-19T01:05:00Z", Who: "stella", Word: "hold", Head: headA, At: "2026-09-19T01:05:00Z", Source: "record", Scope: "docs"},
	}
	reads = []Read{
		{Who: "stella", Verdict: "approve", Head: headA, At: "2026-09-19T02:00:00Z", Scope: "docs", Releases: []string{"record:2026-09-19T01:05:00Z"}},
	}
	unreleased = UnreleasedHolds(holds, reads, headA, "rowan", rs)
	if len(unreleased) != 1 || unreleased[0].ID != "record:2026-09-19T01:00:00Z" {
		t.Fatalf("expected parser hold to remain standing, got: %+v", unreleased)
	}

	// Scenario 3: who=unknown hold released only by may-hold reader naming it in --releases
	holds = []Verdict{
		{ID: "comment:555", Who: "unknown", Word: "hold", Head: headA, At: "2026-09-19T01:00:00Z", Source: "comment-rule"},
	}
	// Approve without --releases does NOT release unknown hold
	reads = []Read{
		{Who: "stella", Verdict: "approve", Head: headA, At: "2026-09-19T02:00:00Z"},
	}
	unreleased = UnreleasedHolds(holds, reads, headA, "rowan", rs)
	if len(unreleased) != 1 {
		t.Fatalf("unscoped approve should not release unknown hold")
	}

	// Approve WITH --releases comment:555 DOES release it
	reads = []Read{
		{Who: "stella", Verdict: "approve", Head: headA, At: "2026-09-19T02:00:00Z", Releases: []string{"comment:555"}},
	}
	unreleased = UnreleasedHolds(holds, reads, headA, "rowan", rs)
	if len(unreleased) != 0 {
		t.Fatalf("approve naming comment:555 should release unknown hold, got: %+v", unreleased)
	}

	// Scenario 4: Stale head approve releases nothing
	holds = []Verdict{
		{ID: "comment:555", Who: "stella", Word: "hold", Head: headA, At: "2026-09-19T01:00:00Z", Source: "comment-rule"},
	}
	reads = []Read{
		{Who: "stella", Verdict: "approve", Head: headA, At: "2026-09-19T02:00:00Z"},
	}
	// PR pushed to headB
	unreleased = UnreleasedHolds(holds, reads, headB, "rowan", rs)
	if len(unreleased) != 1 || !unreleased[0].Carried {
		t.Fatalf("hold should be unreleased and carried on headB: %+v", unreleased)
	}

	// Scenario 5: Short SHA prefix matching
	shortHead := headA[:12]
	reads = []Read{
		{Who: "stella", Verdict: "approve", Head: shortHead, At: "2026-09-19T02:00:00Z"},
	}
	unreleased = UnreleasedHolds(holds, reads, headA, "rowan", rs)
	if len(unreleased) != 0 {
		t.Fatalf("short SHA prefix matching headA should release hold: %+v", unreleased)
	}
}

func TestUnreleasedHoldsPreservesHoldOnEqualTimestamp(t *testing.T) {
	head := "1111111111111111111111111111111111111111"
	rs := sampleReviewers()
	at := "2026-09-19T10:00:00Z"

	// Same friend, current head, APPROVE and HOLD with the same At.
	// HOLD-last rule demands the hold is retained on an equal timestamp tie.
	hold := Verdict{
		ID:     "comment:100",
		Who:    "rowan",
		Word:   "hold",
		Head:   head,
		At:     at,
		Source: "comment-rule",
	}
	read := Read{
		Who:     "rowan",
		Verdict: "approve",
		Head:    head,
		At:      at,
	}

	// Order 1: UnreleasedHolds([hold], [read])
	unreleased1 := UnreleasedHolds([]Verdict{hold}, []Read{read}, head, "author", rs)
	if len(unreleased1) == 0 {
		t.Fatalf("equal timestamp tie must retain HOLD, got released: %+v", unreleased1)
	}

	// Order 2: In UnliftedHolds where verdicts are passed in both orders
	vs1 := []Verdict{
		{Source: "record", Who: "rowan", Word: "approve", Head: head, At: at},
		hold,
	}
	if got := UnliftedHolds(vs1, head, "author", rs); len(got) == 0 {
		t.Fatalf("UnliftedHolds with [approve, hold] at equal At must retain HOLD")
	}

	vs2 := []Verdict{
		hold,
		{Source: "record", Who: "rowan", Word: "approve", Head: head, At: at},
	}
	if got := UnliftedHolds(vs2, head, "author", rs); len(got) == 0 {
		t.Fatalf("UnliftedHolds with [hold, approve] at equal At must retain HOLD")
	}
}

func parseRevTSV(t *testing.T, tsv string) *ReviewerSet {
	t.Helper()
	rs, err := ParseReviewers(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseReviewers failed: %v", err)
	}
	return rs
}

// SPEC-DECIDE reading 3 (lines 1015-1016): A holder absent from the reviewer file
// becomes who=unknown; a who=unknown hold holds, never a drop.
func TestUnliftedHoldsTreatsHolderAbsentFromReviewersFileAsUnknownHold(t *testing.T) {
	t.Parallel()
	rs := parseRevTSV(t, "rowan\trowan-login\tyes\n")
	head := strings.Repeat("a", 40)
	hold := Verdict{
		Source: "record",
		Who:    "stranger",
		Word:   "hold",
		Head:   head,
		At:     "2026-09-19T10:00:00Z",
		ID:     "record:2026-09-19T10:00:00Z",
	}
	got := UnliftedHolds([]Verdict{hold}, head, "author", rs)
	if len(got) != 1 {
		t.Fatalf("hold from holder absent from reviewer file must not be dropped: got %v", got)
	}
	if got[0].Who != "unknown" {
		t.Fatalf("holder absent from reviewer file must become who=unknown, got who=%q", got[0].Who)
	}
}

// SPEC-DECIDE reading 3 (lines 1092-1093, 1533): A dismissal releases nothing.
func TestParseReviewDismissalReleasesNothing(t *testing.T) {
	t.Parallel()
	rs := parseRevTSV(t, "rowan\trowan-login\tyes\n")
	head := strings.Repeat("a", 40)
	v, ok := ParseReview(201, "rowan-login", "", "DISMISSED", head, "2026-09-19T10:00:00Z", rs, "author", head)
	if !ok {
		t.Fatalf("ParseReview must not drop a DISMISSED review")
	}
	if v.Word != "hold" || v.Source != "review" {
		t.Fatalf("DISMISSED review must be a hold, got word=%q source=%q", v.Word, v.Source)
	}
}

// SPEC-DECIDE reading 3 (lines 1038-1039): An untyped comment binds to current head;
// an 8-digit date in body does not bind as a head sha.
func TestCommentDateInBodyDoesNotBindAsHead(t *testing.T) {
	t.Parallel()
	rs := parseRevTSV(t, "rowan\trowan-login\tyes\n")
	currentHead := strings.Repeat("c", 40)
	body := "HOLD: notes from meeting on 20260919 regarding architecture"
	v, ok := ParseComment(301, "rowan-login", body, "2026-09-19T10:00:00Z", rs, "author", currentHead, false)
	if !ok {
		t.Fatalf("ParseComment failed on hold comment")
	}
	if v.Head != currentHead {
		t.Fatalf("untyped comment must bind to currentHead (%s), not date in body (%s)", currentHead, v.Head)
	}
}

// SPEC-DECIDE reading 3 (lines 1017-1019, 1565): The author's login excuses nothing;
// DISPOSITION who=<login> verdict=NOTE on a shared login does not skip the comment.
func TestAuthorLoginExcusesNothingOnSharedLoginNote(t *testing.T) {
	t.Parallel()
	rs := parseRevTSV(t, "alice\tshared-login\tyes\nbob\tshared-login\tyes\n")
	head := strings.Repeat("a", 40)
	// Author is Alice. Comment uses shared-login with who=shared-login:
	body := "DISPOSITION who=shared-login verdict=NOTE\n**HOLD: issue discovered**"
	v, ok := ParseComment(401, "shared-login", body, "2026-09-19T10:00:00Z", rs, "alice", head, false)
	if !ok {
		t.Fatalf("comment with who=shared-login verdict=NOTE must NOT be skipped as author note when author is alice")
	}
	if v.Word != "hold" {
		t.Fatalf("comment with bold HOLD must hold, got %+v", v)
	}
}

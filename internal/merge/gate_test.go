package merge

import (
	"fmt"
	"strings"
	"testing"
)

func TestCountTypedApproves_Basics(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "rowan"

	verdicts := []Verdict{
		// Valid approve from stella
		{ID: "comment:1", Who: "stella", Head: head, Word: "approve", At: "2026-09-19T10:00:00Z", Source: "comment-rule"},
		// Duplicate approve from stella on another comment
		{ID: "comment:2", Who: "stella", Head: head, Word: "approve", At: "2026-09-19T10:05:00Z", Source: "comment-rule"},
		// Approve from author rowan - MUST BE IGNORED
		{ID: "comment:3", Who: "rowan", Head: head, Word: "approve", At: "2026-09-19T10:10:00Z", Source: "comment-rule"},
		// Approve from unauthorized reviewer bot - MUST BE IGNORED
		{ID: "comment:4", Who: "bot", Head: head, Word: "approve", At: "2026-09-19T10:15:00Z", Source: "comment-rule"},
		// Approve on stale head - MUST BE IGNORED
		{ID: "comment:5", Who: "johnny", Head: strings.Repeat("b", 40), Word: "approve", At: "2026-09-19T10:20:00Z", Source: "comment-rule"},
		// Scoped approve - MUST NOT count for unscoped gate
		{ID: "comment:6", Who: "johnny", Head: head, Word: "approve", At: "2026-09-19T10:25:00Z", Scope: "parser", Source: "comment-rule"},
	}

	got := CountTypedApproves(verdicts, head, author, rs)
	if got != 1 {
		t.Fatalf("expected 1 approve (stella), got %d", got)
	}

	st := EvaluateVerdicts(verdicts, head, author, rs)
	if st.Approves != 1 || len(st.Approvers) != 1 || st.Approvers[0] != "stella" {
		t.Fatalf("unexpected standing: %+v", st)
	}
	if st.Held || !st.Satisfied {
		t.Fatalf("expected not held and satisfied, got %+v", st)
	}
}

func TestEvaluateVerdicts_HoldReleaseByTypedApprove(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "author"

	// Stella holds via comment 101, then stella approves releasing comment:101 later
	vs := []Verdict{
		{
			ID:     "comment:101",
			Who:    "stella",
			Head:   head,
			At:     "2026-09-19T10:00:00Z",
			Word:   "hold",
			Source: "comment-rule",
		},
		{
			ID:       "record:102",
			Who:      "stella",
			Head:     head,
			At:       "2026-09-19T11:00:00Z",
			Word:     "approve",
			Source:   "record",
			Releases: []string{"comment:101"},
		},
	}

	st := EvaluateVerdicts(vs, head, author, rs)
	if st.Held {
		t.Fatalf("expected hold to be released, but st.Held is true: %+v", st)
	}
	if st.Approves != 1 {
		t.Fatalf("expected 1 approve, got %d", st.Approves)
	}
	if !st.Satisfied {
		t.Fatalf("expected standing to be satisfied, got %+v", st)
	}
}

func TestEvaluateVerdicts_ActiveHoldBlocksApproval(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "author"

	// Stella holds via comment 101, and approves without releasing 101
	vs := []Verdict{
		{
			ID:     "comment:101",
			Who:    "stella",
			Head:   head,
			At:     "2026-09-19T10:00:00Z",
			Word:   "hold",
			Source: "comment-rule",
		},
		{
			ID:     "comment:102",
			Who:    "stella",
			Head:   head,
			At:     "2026-09-19T11:00:00Z",
			Word:   "approve",
			Scope:  "other",
			Source: "comment-rule",
		},
	}

	st := EvaluateVerdicts(vs, head, author, rs)
	if !st.Held {
		t.Fatalf("expected st.Held to be true")
	}
	if st.Approves != 0 {
		t.Fatalf("expected 0 approves because approver is active holder, got %d", st.Approves)
	}
	if st.Satisfied {
		t.Fatalf("expected gate to not be satisfied")
	}
}

func TestParseReview_IgnoreUntyped(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "author"

	// 1. Untyped comment review body with ignoreUntyped=false -> creates pending hold with review:<id>
	v, ok := ParseReview(201, "stella-astra", "Just a comment with no DISPOSITION", "COMMENTED", head, "2026-09-19T10:00:00Z", rs, author, head, false)
	if !ok {
		t.Fatalf("expected ParseReview to return verdict when ignoreUntyped=false")
	}
	if v.Word != "pending" || v.Source != "comment-pending" || v.ID != "review:201" {
		t.Fatalf("expected pending hold with ID review:201, got %+v", v)
	}

	// 2. Untyped comment review body with ignoreUntyped=true -> ignored completely
	_, ok = ParseReview(201, "stella-astra", "Just a comment with no DISPOSITION", "COMMENTED", head, "2026-09-19T10:00:00Z", rs, author, head, true)
	if ok {
		t.Fatalf("expected ParseReview to ignore untyped review body when ignoreUntyped=true")
	}

	// 3. Typed comment review body with ignoreUntyped=true -> parsed
	body := fmt.Sprintf("DISPOSITION who=stella head=%s verdict=APPROVE releases=comment:99", head)
	v, ok = ParseReview(202, "stella-astra", body, "COMMENTED", head, "2026-09-19T10:00:00Z", rs, author, head, true)
	if !ok {
		t.Fatalf("expected typed review body to be parsed even when ignoreUntyped=true")
	}
	if v.Word != "approve" || v.ID != "review:202" || v.Source != "review" || len(v.Releases) != 1 || v.Releases[0] != "comment:99" {
		t.Fatalf("expected review approve with ID review:202, got %+v", v)
	}

	// 4. APPROVED state review with typed releases in body
	approveBody := "DISPOSITION who=stella releases=review:150 verdict=APPROVE"
	v, ok = ParseReview(203, "stella-astra", approveBody, "APPROVED", head, "2026-09-19T10:00:00Z", rs, author, head, true)
	if !ok {
		t.Fatalf("expected APPROVED review to be parsed")
	}
	if v.Word != "approve" || v.ID != "review:203" || v.Source != "review" || len(v.Releases) != 1 || v.Releases[0] != "review:150" {
		t.Fatalf("expected review approve with releases, got %+v", v)
	}
}

// Mutation test with teeth: verifies multiple mutations fail as expected.
func TestMutation_TypedApproveAndReleaseTeeth(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "author"

	// Baseline scenario:
	// Pending comment hold on PR via comment:100 at 10:00
	// Stella issues typed APPROVE releasing comment:100 at 11:00
	baselineVerdicts := func() []Verdict {
		return []Verdict{
			{
				ID:     "comment:100",
				Who:    "unknown",
				Head:   head,
				At:     "2026-09-19T10:00:00Z",
				Word:   "pending",
				Source: "comment-pending",
			},
			{
				ID:       "record:101",
				Who:      "stella",
				Head:     head,
				At:       "2026-09-19T11:00:00Z",
				Word:     "approve",
				Source:   "record",
				Releases: []string{"comment:100"},
			},
		}
	}

	// Baseline check: must be satisfied
	st := EvaluateVerdicts(baselineVerdicts(), head, author, rs)
	if !st.Satisfied || st.Held || st.Approves != 1 {
		t.Fatalf("baseline assertion failed: %+v", st)
	}

	// Mutation 1: Mutate approve word to "hold"
	{
		vs := baselineVerdicts()
		vs[1].Word = "hold"
		mSt := EvaluateVerdicts(vs, head, author, rs)
		if mSt.Satisfied {
			t.Fatalf("mutation tooth 1 failed: changing approve to hold still resulted in satisfied=true")
		}
		if mSt.Approves != 0 {
			t.Fatalf("mutation tooth 1 failed: changing approve to hold still counted approve")
		}
	}

	// Mutation 2: Mutate release ID so hold 100 is not released
	{
		vs := baselineVerdicts()
		vs[1].Releases = []string{"comment:999"}
		mSt := EvaluateVerdicts(vs, head, author, rs)
		if mSt.Satisfied {
			t.Fatalf("mutation tooth 2 failed: wrong release ID still resulted in satisfied=true")
		}
		if !mSt.Held {
			t.Fatalf("mutation tooth 2 failed: hold was not held")
		}
	}

	// Mutation 3: Mutate approver to author (author self-approval)
	{
		vs := baselineVerdicts()
		vs[1].Who = author
		mSt := EvaluateVerdicts(vs, head, author, rs)
		if mSt.Satisfied {
			t.Fatalf("mutation tooth 3 failed: author self-approval still resulted in satisfied=true")
		}
		if mSt.Approves != 0 {
			t.Fatalf("mutation tooth 3 failed: author self-approval was counted")
		}
	}

	// Mutation 4: Mutate head of approval to mismatched head
	{
		vs := baselineVerdicts()
		vs[1].Head = strings.Repeat("f", 40)
		mSt := EvaluateVerdicts(vs, head, author, rs)
		if mSt.Satisfied {
			t.Fatalf("mutation tooth 4 failed: stale head approval still resulted in satisfied=true")
		}
	}
}

func TestReleasesCrossTypeMatching(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	head := strings.Repeat("a", 40)
	author := "author"

	cases := []struct {
		name       string
		holdID     string
		releasesID string
	}{
		{"exact comment:123", "comment:123", "comment:123"},
		{"numeric 123 releases comment:123", "comment:123", "123"},
		{"comment:123 releases numeric 123", "123", "comment:123"},
		{"exact review:456", "review:456", "review:456"},
		{"numeric 456 releases review:456", "review:456", "456"},
		{"comment:456 cross-releases review:456", "review:456", "comment:456"},
		{"review:456 cross-releases comment:456", "comment:456", "review:456"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vs := []Verdict{
				{
					ID:     tc.holdID,
					Who:    "unknown",
					Head:   head,
					At:     "2026-09-19T10:00:00Z",
					Word:   "pending",
					Source: "comment-pending",
				},
				{
					ID:       "record:999",
					Who:      "stella",
					Head:     head,
					At:       "2026-09-19T11:00:00Z",
					Word:     "approve",
					Source:   "record",
					Releases: []string{tc.releasesID},
				},
			}
			st := EvaluateVerdicts(vs, head, author, rs)
			if !st.Satisfied || st.Held || st.Approves != 1 {
				t.Fatalf("expected release of %s by %s, got: %+v", tc.holdID, tc.releasesID, st)
			}
		})
	}
}

package workreconcile

import (
	"strings"
	"testing"
)

func TestReconciliationClean(t *testing.T) {
	manifest := &CaptureManifest{
		Provider:    "github",
		Repo:        "mas-bandwidth/nova-tools",
		TotalIssues: 3,
		Issues: []CapturedIssue{
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       1,
				Title:        "First",
				State:        "open",
				Revision:     "rev-1",
				CommentCount: 2,
				Labels:       []string{"bug", "p1"},
			},
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       2,
				Title:        "Second",
				State:        "closed",
				Revision:     "rev-2",
				CommentCount: 5,
				Labels:       []string{"enhancement"},
			},
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       3,
				Title:        "Third",
				State:        "open",
				Revision:     "rev-3",
				CommentCount: 0,
				Labels:       nil,
			},
		},
	}

	store := NewStore()
	for _, issue := range manifest.Issues {
		uid, _ := MintUID()
		store.AddNode(&WorkNode{
			UID:          uid,
			ID:           issue.CanonicalKey(),
			Provider:     issue.Provider,
			Repo:         issue.Repo,
			IssueNumber:  issue.Number,
			Title:        issue.Title,
			State:        issue.State,
			Revision:     issue.Revision,
			CommentCount: issue.CommentCount,
			Labels:       issue.Labels,
		})
	}

	engine := NewReconciliationEngine()
	report, err := engine.Reconcile(manifest, store)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	if !report.Passed {
		t.Fatalf("expected report to pass, but got discrepancies: %+v", report.Discrepancies)
	}
	if len(report.Discrepancies) != 0 {
		t.Fatalf("expected 0 discrepancies, got %d", len(report.Discrepancies))
	}
	if report.MatchedCount != 3 {
		t.Fatalf("expected matched count 3, got %d", report.MatchedCount)
	}

	text := report.ReportText()
	if !strings.Contains(text, "RECONCILE RESULT repo=mas-bandwidth/nova-tools PASS") {
		t.Fatalf("expected PASS in report text, got:\n%s", text)
	}
}

func TestReconciliationDiscrepanciesNeverSummarized(t *testing.T) {
	manifest := &CaptureManifest{
		Provider:    "github",
		Repo:        "mas-bandwidth/nova-tools",
		TotalIssues: 4,
		Issues: []CapturedIssue{
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       10,
				Title:        "Issue 10",
				State:        "open",
				Revision:     "rev-10-new",
				CommentCount: 3,
				Labels:       []string{"triage"},
			},
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       20,
				Title:        "Issue 20",
				State:        "open",
				Revision:     "rev-20",
				CommentCount: 8, // store has 5
				Labels:       []string{"bug", "p0"},
			},
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       30,
				Title:        "Issue 30",
				State:        "closed", // store has open
				Revision:     "rev-30",
				CommentCount: 1,
				Labels:       []string{"docs", "approved"}, // store has only docs
			},
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       40, // missing from store
				Title:        "Issue 40",
				State:        "open",
				Revision:     "rev-40",
				CommentCount: 0,
			},
		},
	}

	store := NewStore()
	// Issue 10: revision mismatch
	uid1, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:          uid1,
		Repo:         "mas-bandwidth/nova-tools",
		IssueNumber:  10,
		Title:        "Issue 10",
		State:        "open",
		Revision:     "rev-10-old",
		CommentCount: 3,
		Labels:       []string{"triage"},
	})
	// Issue 20: comment count mismatch
	uid2, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:          uid2,
		Repo:         "mas-bandwidth/nova-tools",
		IssueNumber:  20,
		Title:        "Issue 20",
		State:        "open",
		Revision:     "rev-20",
		CommentCount: 5,
		Labels:       []string{"bug", "p0"},
	})
	// Issue 30: state mismatch and label mismatch
	uid3, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:          uid3,
		Repo:         "mas-bandwidth/nova-tools",
		IssueNumber:  30,
		Title:        "Issue 30",
		State:        "open",
		Revision:     "rev-30",
		CommentCount: 1,
		Labels:       []string{"docs"},
	})
	// Issue 50: extra in store (not in capture)
	uid5, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:          uid5,
		Repo:         "mas-bandwidth/nova-tools",
		IssueNumber:  50,
		Title:        "Issue 50 Extra",
		State:        "open",
		Revision:     "rev-50",
		CommentCount: 0,
	})

	engine := NewReconciliationEngine()
	report, err := engine.Reconcile(manifest, store)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	if report.Passed {
		t.Fatal("expected report to FAIL")
	}

	// Expected discrepancies:
	// issue 10: revision mismatch
	// issue 20: comment count mismatch
	// issue 30: label set mismatch AND state mismatch (2 separate discrepancies!)
	// issue 40: missing in store
	// issue 50: extra in store
	// Total discrepancies = 6
	if len(report.Discrepancies) != 6 {
		t.Fatalf("expected exactly 6 discrepancies, got %d:\n%+v", len(report.Discrepancies), report.Discrepancies)
	}

	// Verify deterministic sorting: issue numbers must be 10, 20, 30, 30, 40, 50
	expectedIssues := []int{10, 20, 30, 30, 40, 50}
	for i, d := range report.Discrepancies {
		if d.IssueNumber != expectedIssues[i] {
			t.Errorf("at index %d: expected issue #%d, got #%d", i, expectedIssues[i], d.IssueNumber)
		}
	}

	// Verify report text never summarizes away differences
	text := report.ReportText()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	// Header + 6 discrepancy lines + Footer = 8 lines
	if len(lines) != 8 {
		t.Fatalf("expected 8 lines in report text (every discrepancy listed explicitly), got %d:\n%s", len(lines), text)
	}

	if !strings.Contains(text, "issue=10 kind=revision_mismatch") {
		t.Errorf("missing issue 10 discrepancy in text:\n%s", text)
	}
	if !strings.Contains(text, "issue=20 kind=comment_count_mismatch") {
		t.Errorf("missing issue 20 discrepancy in text:\n%s", text)
	}
	if !strings.Contains(text, "issue=30 kind=label_set_mismatch") {
		t.Errorf("missing issue 30 label discrepancy in text:\n%s", text)
	}
	if !strings.Contains(text, "issue=30 kind=state_mismatch") {
		t.Errorf("missing issue 30 state discrepancy in text:\n%s", text)
	}
	if !strings.Contains(text, "issue=40 kind=missing_in_store") {
		t.Errorf("missing issue 40 discrepancy in text:\n%s", text)
	}
	if !strings.Contains(text, "issue=50 kind=extra_in_store") {
		t.Errorf("missing issue 50 discrepancy in text:\n%s", text)
	}
	if !strings.Contains(text, "RECONCILE RESULT repo=mas-bandwidth/nova-tools FAIL discrepancies=6") {
		t.Errorf("missing FAIL footer in text:\n%s", text)
	}
}

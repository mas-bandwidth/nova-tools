package workreconcile

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestProvingRunOrchestratorEndToEnd(t *testing.T) {
	ctx := context.Background()

	// 1. Create realistic capture manifests for nova-tools and schema
	ntManifest := generateTestManifest("mas-bandwidth/nova-tools", 60)
	// Mark issue 1945 as closed in nova-tools to reflect landed work
	for i := range ntManifest.Issues {
		if ntManifest.Issues[i].Number == 1945 {
			ntManifest.Issues[i].State = "closed"
		}
	}
	// Add #1945 specifically if count is smaller
	ntManifest.Issues = append(ntManifest.Issues, CapturedIssue{
		Provider:     "github",
		Repo:         "mas-bandwidth/nova-tools",
		Number:       1945,
		Title:        "fill: fail-closed load, capacity from the slot store",
		State:        "closed",
		Revision:     "rev-1945",
		CommentCount: 12,
		Labels:       []string{"fill", "landed"},
	})
	ntManifest.TotalIssues = len(ntManifest.Issues)

	schemaManifest := generateTestManifest("mas-bandwidth/schema", 20)
	schemaManifest.Issues = append(schemaManifest.Issues, CapturedIssue{
		Provider:     "github",
		Repo:         "mas-bandwidth/schema",
		Number:       1376,
		Title:        "go leg: card-added test",
		State:        "open",
		Revision:     "rev-1376",
		CommentCount: 4,
		Labels:       []string{"go"},
	})
	schemaManifest.TotalIssues = len(schemaManifest.Issues)

	// Try reading the real sprint priorities file if present, else fallback to sample
	sprintText := sampleSprintText
	if data, err := os.ReadFile("/Users/glenn/rowan-new/reports/sprint-priorities-2026-09-20.txt"); err == nil {
		sprintText = string(data)
	}

	orchestrator := NewProvingRunOrchestrator(20)

	cfg := ProvingRunConfig{
		NovaToolsManifest: ntManifest,
		SchemaManifest:    schemaManifest,
		SprintText:        sprintText,
		BatchSize:         20,
		SimulateInterrupt: true,
	}

	verdict, err := orchestrator.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Proving run execution failed: %v", err)
	}

	if !verdict.InterruptionPassed {
		t.Fatalf("interruption proof failed: %s", verdict.InterruptionDetail)
	}
	if !verdict.Passed {
		t.Fatalf("proving run overall did not pass:\n%s", verdict.SummaryText())
	}

	if len(verdict.Reports) != 2 {
		t.Fatalf("expected 2 reconciliation reports (nova-tools and schema), got %d", len(verdict.Reports))
	}

	for _, r := range verdict.Reports {
		if !r.Passed {
			t.Errorf("report for repo %s failed with %d discrepancies:\n%s", r.Repo, len(r.Discrepancies), r.ReportText())
		}
	}

	if verdict.SprintResult == nil {
		t.Fatal("sprint result is nil")
	}

	// Print the full summary text for visibility in test log
	summary := verdict.SummaryText()
	t.Logf("\n%s", summary)

	if !strings.Contains(summary, "PROVING RUN OVERALL: PASS") {
		t.Fatalf("expected PROVING RUN OVERALL: PASS in summary, got:\n%s", summary)
	}
}

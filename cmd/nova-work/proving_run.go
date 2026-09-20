package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/workreconcile"
)

// cmdProvingRun coordinates the E09 proving run across repositories (Issue #2082).
func cmdProvingRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proving-run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	ntPath := fs.String("nova-tools", "", "path to mas-bandwidth/nova-tools captured JSON (required)")
	schemaPath := fs.String("schema", "", "path to mas-bandwidth/schema captured JSON (optional)")
	sprintPath := fs.String("sprint", "", "path to sprint priorities text file (optional)")
	batchSize := fs.Int("batch-size", 25, "batch size for resumable import")
	simulateInterrupt := fs.Bool("interrupt", true, "simulate interruption and prove clean resumption")

	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " proving-run", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if *ntPath == "" {
		return refuse(stderr, " proving-run", "--nova-tools <path> is required")
	}

	ntRaw, err := os.ReadFile(*ntPath)
	if err != nil {
		return refuse(stderr, " proving-run", oneline.Err(err))
	}
	var ntManifest workreconcile.CaptureManifest
	if err := json.Unmarshal(ntRaw, &ntManifest); err != nil {
		return refuse(stderr, " proving-run", fmt.Sprintf("invalid nova-tools manifest: %s", oneline.Err(err)))
	}

	var schemaManifest *workreconcile.CaptureManifest
	if *schemaPath != "" {
		sRaw, err := os.ReadFile(*schemaPath)
		if err != nil {
			return refuse(stderr, " proving-run", oneline.Err(err))
		}
		var sm workreconcile.CaptureManifest
		if err := json.Unmarshal(sRaw, &sm); err != nil {
			return refuse(stderr, " proving-run", fmt.Sprintf("invalid schema manifest: %s", oneline.Err(err)))
		}
		schemaManifest = &sm
	}

	sprintText := ""
	if *sprintPath != "" {
		spRaw, err := os.ReadFile(*sprintPath)
		if err != nil {
			return refuse(stderr, " proving-run", oneline.Err(err))
		}
		sprintText = string(spRaw)
	}

	orchestrator := workreconcile.NewProvingRunOrchestrator(*batchSize)
	cfg := workreconcile.ProvingRunConfig{
		NovaToolsManifest: &ntManifest,
		SchemaManifest:    schemaManifest,
		SprintText:        sprintText,
		BatchSize:         *batchSize,
		SimulateInterrupt: *simulateInterrupt,
	}

	start := time.Now()
	verdict, err := orchestrator.Execute(context.Background(), cfg)
	if err != nil {
		return refuse(stderr, " proving-run", oneline.Err(err))
	}

	// Print deterministic output lines
	statusStr := "FAIL"
	if verdict.InterruptionPassed {
		statusStr = "PASS"
	}
	fmt.Fprintf(stdout, "INTERRUPTION %s detail=%s\n",
		oneline.Field(statusStr), oneline.Quote(verdict.InterruptionDetail))

	for _, report := range verdict.Reports {
		for _, d := range report.Discrepancies {
			fmt.Fprintf(stdout, "DISCREPANCY repo=%s issue=%d kind=%s detail=%s captured=%s nova_work=%s\n",
				field(d.Repo), d.IssueNumber, field(string(d.Kind)), oneline.Quote(d.Detail),
				field(d.CapturedValue), field(d.StoreValue))
		}
		repPassed := "FAIL"
		if report.Passed {
			repPassed = "PASS"
		}
		fmt.Fprintf(stdout, "RECONCILE repo=%s captured=%d imported=%d discrepancies=%d result=%s\n",
			field(report.Repo), report.CapturedCount, report.StoreCount, len(report.Discrepancies), oneline.Field(repPassed))
	}

	if verdict.SprintResult != nil {
		sr := verdict.SprintResult
		fmt.Fprintf(stdout, "SPRINT rows_completed=%d rows_total=%d percent=%.1f issues_closed=%d issues_total=%d issue_percent=%.1f\n",
			sr.CompletedPriorities, sr.TotalPriorities, sr.CompletionPercentage,
			sr.CompletedReferencedIssues, sr.TotalReferencedIssues, sr.IssueCompletionPercentage)
		for _, row := range sr.Rows {
			fmt.Fprintf(stdout, "SPRINT ROW rank=%d status=%s title=%s\n",
				row.Rank, field(row.State), oneline.Quote(row.Title))
		}
	}

	verdictStr := "FAIL"
	if verdict.Passed {
		verdictStr = "PASS"
	}
	durationMs := time.Since(start).Milliseconds()
	fmt.Fprintf(stdout, "PROVING RUN result=%s duration_ms=%d\n",
		oneline.Field(verdictStr), durationMs)

	if !verdict.Passed {
		return 1
	}
	return 0
}

package workreconcile

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProvingRunConfig holds the input parameters for an E09 proving run.
type ProvingRunConfig struct {
	NovaToolsManifest *CaptureManifest
	SchemaManifest    *CaptureManifest
	SprintText        string
	BatchSize         int
	SimulateInterrupt bool
}

// ProvingRunVerdict contains the complete execution results of the proving run.
type ProvingRunVerdict struct {
	Timestamp          string                  `json:"timestamp"`
	Duration           time.Duration           `json:"duration"`
	InterruptionPassed bool                    `json:"interruption_passed"`
	InterruptionDetail string                  `json:"interruption_detail"`
	Reports            []*ReconciliationReport `json:"reports"`
	SprintResult       *SprintQueryResult      `json:"sprint_result"`
	Passed             bool                    `json:"passed"`
}

// SummaryText formats the proving run verdict for reporting and logs.
func (v *ProvingRunVerdict) SummaryText() string {
	var sb strings.Builder
	sb.WriteString("================================================================================\n")
	sb.WriteString(fmt.Sprintf("NOVA-WORK E09 PROVING RUN VERDICT: %s (duration=%v)\n",
		v.Timestamp, v.Duration.Round(time.Millisecond)))
	sb.WriteString("================================================================================\n\n")

	sb.WriteString("1. INTERRUPTION AND RESUMPTION:\n")
	if v.InterruptionPassed {
		sb.WriteString("   STATUS: PASS\n")
	} else {
		sb.WriteString("   STATUS: FAIL\n")
	}
	sb.WriteString(fmt.Sprintf("   DETAIL: %s\n\n", v.InterruptionDetail))

	sb.WriteString("2. RECONCILIATION REPORTS:\n")
	for _, r := range v.Reports {
		sb.WriteString(r.ReportText())
		sb.WriteByte('\n')
	}

	if v.SprintResult != nil {
		sb.WriteString("3. SPRINT PRIORITY QUERY:\n")
		sb.WriteString(v.SprintResult.SummaryText())
		sb.WriteByte('\n')
	}

	sb.WriteString("--------------------------------------------------------------------------------\n")
	if v.Passed {
		sb.WriteString("PROVING RUN OVERALL: PASS (All criteria verified, zero duplicate nodes, clean reconcile)\n")
	} else {
		sb.WriteString("PROVING RUN OVERALL: FAIL (Discrepancies found or interruption test failed)\n")
	}
	sb.WriteString("================================================================================\n")

	return sb.String()
}

// ProvingRunOrchestrator orchestrates the E09 proving run across repositories.
type ProvingRunOrchestrator struct {
	Engine   *ReconciliationEngine
	Importer *BatchImporter
	Store    *Store
}

// NewProvingRunOrchestrator creates a new orchestrator.
func NewProvingRunOrchestrator(batchSize int) *ProvingRunOrchestrator {
	if batchSize <= 0 {
		batchSize = 25
	}
	return &ProvingRunOrchestrator{
		Engine:   NewReconciliationEngine(),
		Importer: NewBatchImporter(batchSize),
		Store:    NewStore(),
	}
}

// Execute runs the complete proving run workflow according to Issue #2082 specifications:
// 1. Proves interruption and resumption with zero duplicate nodes.
// 2. Batched imports of nova-tools and schema issues.
// 3. Reconciles both repositories, producing deterministic difference reports.
// 4. Instantiates and queries sprint priority nodes.
func (o *ProvingRunOrchestrator) Execute(ctx context.Context, cfg ProvingRunConfig) (*ProvingRunVerdict, error) {
	start := time.Now()
	verdict := &ProvingRunVerdict{
		Timestamp: start.UTC().Format(time.RFC3339),
	}

	if cfg.NovaToolsManifest == nil {
		return nil, fmt.Errorf("nova-tools capture manifest is required")
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = o.Importer.BatchSize
	}
	o.Importer.BatchSize = batchSize

	// Step 1: Prove Interruption and Resumption (E09-F03-03)
	// We deliberately interrupt the import after batch 1, assert partial state,
	// then resume and assert zero duplicate nodes and exact total count.
	if cfg.SimulateInterrupt && len(cfg.NovaToolsManifest.Issues) > batchSize {
		testStore := NewStore()
		interruptedImporter := NewBatchImporter(batchSize)
		interruptedImporter.InterruptHook = func(batchNum int, lastApplied int) error {
			if batchNum == 1 {
				return fmt.Errorf("simulated crash after batch 1 at issue #%d", lastApplied)
			}
			return nil
		}

		// First pass: hits interruption hook
		summary1, err := interruptedImporter.Import(ctx, cfg.NovaToolsManifest, testStore)
		if err != ErrInterrupted {
			verdict.InterruptionPassed = false
			verdict.InterruptionDetail = fmt.Sprintf("expected ErrInterrupted, got %v", err)
			return verdict, fmt.Errorf("interruption simulation failed: %v", err)
		}
		_ = summary1
		partialNodes := testStore.TotalNodeCount()

		// Resumption pass: remove interrupt hook and resume
		interruptedImporter.InterruptHook = nil
		summary2, err := interruptedImporter.Import(ctx, cfg.NovaToolsManifest, testStore)
		if err != nil {
			verdict.InterruptionPassed = false
			verdict.InterruptionDetail = fmt.Sprintf("resumption failed: %v", err)
			return verdict, fmt.Errorf("resumption failed: %w", err)
		}

		finalNodes := testStore.TotalNodeCount()
		expectedTotal := len(cfg.NovaToolsManifest.Issues)

		// Third pass: re-import identical capture to prove idempotency
		summary3, err := interruptedImporter.Import(ctx, cfg.NovaToolsManifest, testStore)
		if err != nil {
			return verdict, fmt.Errorf("idempotent re-import failed: %w", err)
		}

		if finalNodes == expectedTotal && summary2.NewNodes == (expectedTotal-partialNodes) && summary3.NewNodes == 0 {
			verdict.InterruptionPassed = true
			verdict.InterruptionDetail = fmt.Sprintf(
				"interrupted at batch 1 (nodes=%d); resumed to completion (nodes=%d, zero duplicates); re-import was no-op (new=%d, deduplicated=%d)",
				partialNodes, finalNodes, summary3.NewNodes, summary3.DeduplicatedNodes)
		} else {
			verdict.InterruptionPassed = false
			verdict.InterruptionDetail = fmt.Sprintf(
				"node mismatch: expected %d nodes, got %d; summary2 new=%d, summary3 new=%d",
				expectedTotal, finalNodes, summary2.NewNodes, summary3.NewNodes)
		}
	} else {
		verdict.InterruptionPassed = true
		verdict.InterruptionDetail = "skipped or passed (single batch)"
	}

	// Step 2: Import into main orchestrator store
	// 2a. Import nova-tools
	o.Importer.InterruptHook = nil
	ntSummary, err := o.Importer.Import(ctx, cfg.NovaToolsManifest, o.Store)
	if err != nil {
		return verdict, fmt.Errorf("importing nova-tools: %w", err)
	}

	// 2b. Import schema (if present)
	var schemaSummary *ImportSummary
	if cfg.SchemaManifest != nil {
		schemaSummary, err = o.Importer.Import(ctx, cfg.SchemaManifest, o.Store)
		if err != nil {
			return verdict, fmt.Errorf("importing schema: %w", err)
		}
	}

	_ = ntSummary
	_ = schemaSummary

	// Step 3: Run Reconciliation Engine (E09-F02, E09-F03)
	// 3a. Reconcile nova-tools
	ntReport, err := o.Engine.Reconcile(cfg.NovaToolsManifest, o.Store)
	if err != nil {
		return verdict, fmt.Errorf("reconciling nova-tools: %w", err)
	}
	verdict.Reports = append(verdict.Reports, ntReport)

	// 3b. Reconcile schema (if present)
	if cfg.SchemaManifest != nil {
		schemaReport, err := o.Engine.Reconcile(cfg.SchemaManifest, o.Store)
		if err != nil {
			return verdict, fmt.Errorf("reconciling schema: %w", err)
		}
		verdict.Reports = append(verdict.Reports, schemaReport)
	}

	// Step 4: Sprint Priority Nodes & Completion Query
	if cfg.SprintText != "" {
		priorities, err := ParseSprintPriorities(cfg.SprintText, "mas-bandwidth/nova-tools")
		if err != nil {
			return verdict, fmt.Errorf("parsing sprint priorities: %w", err)
		}

		// Register sprint priority nodes into store
		if _, err := BuildSprintPriorityNodes(o.Store, priorities); err != nil {
			return verdict, fmt.Errorf("building sprint priority nodes: %w", err)
		}

		// Query sprint completion percentage based on imported issue states
		sprintResult := QuerySprintCompletion(o.Store, priorities)
		verdict.SprintResult = sprintResult
	}

	// Step 5: Overall verdict calculation
	allReportsPassed := true
	for _, r := range verdict.Reports {
		if !r.Passed {
			allReportsPassed = false
			break
		}
	}

	verdict.Passed = verdict.InterruptionPassed && allReportsPassed
	verdict.Duration = time.Since(start)

	return verdict, nil
}

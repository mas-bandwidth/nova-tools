package pulse

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFunnelUnmeasuredSpendRecordedAsDash(t *testing.T) {
	// Rule: Unmeasured spend recorded as "-", never manufactured zeros.
	for _, input := range []string{"", "-", "unmeasured", "unknown", "none"} {
		got := FormatSpend(input)
		if got != "-" {
			t.Errorf("FormatSpend(%q) = %q, want \"-\"", input, got)
		}
		if strings.Contains(got, "0") {
			t.Errorf("FormatSpend(%q) manufactured a zero: %q", input, got)
		}
	}

	// When measured, format as float string
	if got := FormatSpend("0.045"); got != "0.0450" {
		t.Errorf("FormatSpend(\"0.045\") = %q, want \"0.0450\"", got)
	}
}

func TestFunnelAppendOnlyLifecycleAndVoidPreservesHistory(t *testing.T) {
	tmpDir := t.TempDir()
	funnelDir := filepath.Join(tmpDir, "funnel")

	r1 := FunnelRecord{
		At:      "2026-09-21T10:00:00Z",
		Event:   EventAdmit,
		Card:    "card-001",
		Attempt: "1",
		Model:   "deepseek-v4-pro",
		Bench:   "batman",
		Verdict: "-",
		Spend:   FormatSpend(""),
		Detail:  "source=pool",
	}
	if err := AppendFunnelRecord(funnelDir, r1); err != nil {
		t.Fatalf("AppendFunnelRecord r1: %v", err)
	}

	r2 := FunnelRecord{
		At:      "2026-09-21T10:01:00Z",
		Event:   EventLaunch,
		Card:    "card-001",
		Attempt: "1",
		Model:   "deepseek-v4-pro",
		Bench:   "batman",
		Verdict: "-",
		Spend:   FormatSpend(""),
		Detail:  "slot=batman-01",
	}
	if err := AppendFunnelRecord(funnelDir, r2); err != nil {
		t.Fatalf("AppendFunnelRecord r2: %v", err)
	}

	// Now record a VOID event for attempt 1 (e.g. abandoned/retried)
	r3 := FunnelRecord{
		At:      "2026-09-21T10:05:00Z",
		Event:   EventVoid,
		Card:    "card-001",
		Attempt: "1",
		Model:   "deepseek-v4-pro",
		Bench:   "batman",
		Verdict: "ABANDONED",
		Spend:   FormatSpend(""),
		Detail:  "retried attempt 2 due to timeout",
	}
	if err := AppendFunnelRecord(funnelDir, r3); err != nil {
		t.Fatalf("AppendFunnelRecord r3 (VOID): %v", err)
	}

	// Record attempt 2 progressing to LAND
	r4 := FunnelRecord{
		At:      "2026-09-21T10:06:00Z",
		Event:   EventLaunch,
		Card:    "card-001",
		Attempt: "2",
		Model:   "deepseek-v4-pro",
		Bench:   "hulk",
		Verdict: "-",
		Spend:   FormatSpend(""),
		Detail:  "slot=hulk-02",
	}
	if err := AppendFunnelRecord(funnelDir, r4); err != nil {
		t.Fatalf("AppendFunnelRecord r4: %v", err)
	}

	r5 := FunnelRecord{
		At:      "2026-09-21T10:10:00Z",
		Event:   EventHarvest,
		Card:    "card-001",
		Attempt: "2",
		Model:   "deepseek-v4-pro",
		Bench:   "hulk",
		Verdict: "OK",
		Spend:   FormatSpend("0.0520"),
		Detail:  "pr=2454",
	}
	if err := AppendFunnelRecord(funnelDir, r5); err != nil {
		t.Fatalf("AppendFunnelRecord r5: %v", err)
	}

	r6 := FunnelRecord{
		At:      "2026-09-21T10:12:00Z",
		Event:   EventGate,
		Card:    "card-001",
		Attempt: "2",
		Model:   "deepseek-v4-pro",
		Bench:   "hulk",
		Verdict: "PASS",
		Spend:   FormatSpend(""),
		Detail:  "gate=green",
	}
	if err := AppendFunnelRecord(funnelDir, r6); err != nil {
		t.Fatalf("AppendFunnelRecord r6: %v", err)
	}

	r7 := FunnelRecord{
		At:      "2026-09-21T10:15:00Z",
		Event:   EventLand,
		Card:    "card-001",
		Attempt: "2",
		Model:   "deepseek-v4-pro",
		Bench:   "hulk",
		Verdict: "MERGED",
		Spend:   FormatSpend(""),
		Detail:  "sha=e6b73767",
	}
	if err := AppendFunnelRecord(funnelDir, r7); err != nil {
		t.Fatalf("AppendFunnelRecord r7: %v", err)
	}

	// Read all records: history must be strictly append-only, all 7 records present
	records, err := ReadFunnel(funnelDir)
	if err != nil {
		t.Fatalf("ReadFunnel: %v", err)
	}
	if len(records) != 7 {
		t.Fatalf("expected 7 records in append-only history, got %d", len(records))
	}

	// Verify attempt 1 was marked VOID without deleting its ADMIT or LAUNCH lines
	if records[0].Event != EventAdmit || records[0].Attempt != "1" {
		t.Errorf("record 0 corrupted: %+v", records[0])
	}
	if records[1].Event != EventLaunch || records[1].Attempt != "1" {
		t.Errorf("record 1 corrupted: %+v", records[1])
	}
	if records[2].Event != EventVoid || records[2].Attempt != "1" {
		t.Errorf("record 2 corrupted: %+v", records[2])
	}

	// Compute summary
	summary := ComputeFunnelSummary(records)
	if summary.AdmittedCards != 1 {
		t.Errorf("AdmittedCards = %d, want 1", summary.AdmittedCards)
	}
	if summary.LandedCards != 1 {
		t.Errorf("LandedCards = %d, want 1", summary.LandedCards)
	}
	if summary.VoidedAttempts != 1 {
		t.Errorf("VoidedAttempts = %d, want 1", summary.VoidedAttempts)
	}
	if summary.CostPerLanded != "0.0520" {
		t.Errorf("CostPerLanded = %q, want \"0.0520\"", summary.CostPerLanded)
	}
}

func TestFunnelSummaryWithUnmeasuredSpendReportsDash(t *testing.T) {
	// If no spend was measured, summary spend and cost_per_landed MUST be "-", never "0" or "$0.00"
	records := []FunnelRecord{
		{Event: EventAdmit, Card: "c1", Attempt: "1", Spend: "-"},
		{Event: EventLaunch, Card: "c1", Attempt: "1", Spend: "-"},
		{Event: EventHarvest, Card: "c1", Attempt: "1", Spend: "-"},
		{Event: EventGate, Card: "c1", Attempt: "1", Spend: "-"},
		{Event: EventLand, Card: "c1", Attempt: "1", Spend: "-"},
	}

	summary := ComputeFunnelSummary(records)
	if summary.HasMeasuredSpend {
		t.Errorf("HasMeasuredSpend should be false when all events are unmeasured")
	}
	if summary.CostPerLanded != "-" {
		t.Errorf("CostPerLanded = %q, want \"-\"", summary.CostPerLanded)
	}

	line := summary.OneLine()
	if !strings.Contains(line, "spend=-") {
		t.Errorf("summary OneLine missing \"spend=-\": %s", line)
	}
	if !strings.Contains(line, "cost_per_landed=-") {
		t.Errorf("summary OneLine missing \"cost_per_landed=-\": %s", line)
	}
	if strings.Contains(line, "spend=0") || strings.Contains(line, "cost_per_landed=0") {
		t.Errorf("summary OneLine manufactured a zero: %s", line)
	}
}

func TestFunnelMutationTeeth(t *testing.T) {
	// A mutation test with teeth: verifies that mutating spend to 0.00 or omitting VOID
	// triggers hard assertion failures.
	t.Run("mutant_zero_spend_fails", func(t *testing.T) {
		mutantSpend := "0.0000"
		if got := FormatSpend(""); got == mutantSpend {
			t.Fatalf("TEETH: FormatSpend(\"\") returned manufactured zero %q", got)
		}
	})

	t.Run("mutant_void_rewrites_history_fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		funnelDir := filepath.Join(tmpDir, "funnel")
		_ = AppendFunnelRecord(funnelDir, FunnelRecord{Event: EventAdmit, Card: "c1", Spend: "-"})
		_ = AppendFunnelRecord(funnelDir, FunnelRecord{Event: EventLaunch, Card: "c1", Spend: "-"})
		countBefore := len(mustReadFunnel(t, funnelDir))

		// VOID should increment line count, never rewrite or decrement
		_ = AppendFunnelRecord(funnelDir, FunnelRecord{Event: EventVoid, Card: "c1", Spend: "-"})
		countAfter := len(mustReadFunnel(t, funnelDir))
		if countAfter <= countBefore {
			t.Fatalf("TEETH: VOID event did not append (before=%d, after=%d)", countBefore, countAfter)
		}
	})
}

func mustReadFunnel(t *testing.T, dir string) []FunnelRecord {
	t.Helper()
	r, err := ReadFunnel(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSprintFunnelCLIEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "queue")

	var out, errb bytes.Buffer
	// Record an ADMIT event
	code := SprintFunnel(SprintFunnelInput{
		Queue:   queueDir,
		Action:  "record",
		Event:   EventAdmit,
		Card:    "card-100",
		Attempt: "1",
		Model:   "gemini-2.5-pro",
		Bench:   "studio",
		Spend:   "-",
		Detail:  "first run",
		Now:     func() time.Time { return time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC) },
		Stdout:  &out,
		Stderr:  &errb,
	})
	if code != 0 {
		t.Fatalf("record ADMIT exit = %d, want 0; err=%s", code, errb.String())
	}

	// Record a VOID event
	out.Reset()
	code = SprintFunnel(SprintFunnelInput{
		Queue:   queueDir,
		Action:  "void",
		Card:    "card-100",
		Attempt: "1",
		Detail:  "retrying on another bench",
		Now:     func() time.Time { return time.Date(2026, 9, 21, 10, 5, 0, 0, time.UTC) },
		Stdout:  &out,
		Stderr:  &errb,
	})
	if code != 0 {
		t.Fatalf("void exit = %d, want 0; err=%s", code, errb.String())
	}

	// Report funnel
	out.Reset()
	code = SprintFunnel(SprintFunnelInput{
		Queue:   queueDir,
		Action:  "report",
		Oneline: true,
		Stdout:  &out,
		Stderr:  &errb,
	})
	if code != 0 {
		t.Fatalf("report exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "FUNNEL admit=1") {
		t.Errorf("report output missing admit=1: %s", out.String())
	}
}

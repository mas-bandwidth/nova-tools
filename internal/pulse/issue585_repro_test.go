package pulse

// Nova-tools #585: the learned admission checklist and read RESULT scope.
// This file holds the reproducing tests for the issue: read cards must state their scope
// with "not checked: <list>", and the probe must refuse cards before launch.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadResultStatesNotChecked verifies that a read card's RESULT.md carries
// "not checked: <list>" so "review passed" never loses the kind and scope of evidence.
// Red test: the read.md template does not instruct the worker to write this line.
func TestReadResultStatesNotChecked(t *testing.T) {
	// Read the read.md template
	raw, err := os.ReadFile(filepath.Join("..", "..", "cmd", "nova-pulse", "testdata", "templates", "read.md"))
	if err != nil {
		t.Fatalf("read.md template missing: %s", err)
	}
	content := string(raw)
	
	// The template must instruct the worker to write "not checked: <list>" in RESULT.md
	if !strings.Contains(content, "not checked:") {
		t.Errorf("read.md template does not instruct worker to write 'not checked: <list>' in RESULT.md")
	}
}

// TestProbeRefusesBeforeLaunch verifies that a card carrying a known class is refused
// with no card written and exit 1. This is the red test for the probe refusing before launch.
func TestProbeRefusesBeforeLaunch(t *testing.T) {
	// Create a card that would fail the admission check
	card := "STEP 1. cd ../scratch and write\nRESULT.md\n"
	
	// Create a history with the admission class
	historyPath := filepath.Join(t.TempDir(), "abstain-history.tsv")
	if err := os.WriteFile(historyPath, []byte("admission\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	
	var out, errb bytes.Buffer
	code := Probe(ProbeInput{
		Label:   "test-card",
		Card:    card,
		History: historyPath,
		Stdout:  &out,
		Stderr:  &errb,
	})
	
	// The probe must refuse the card (exit 1)
	if code != 1 {
		t.Fatalf("probe admitted a card with a known failure class: code=%d, want 1; stderr=%s", code, errb.String())
	}
	
	// The refusal must name the class
	if !strings.Contains(errb.String(), "class=admission") {
		t.Fatalf("refusal does not name the class: %q", errb.String())
	}
}

// TestProbeCountsByClassPerDay verifies the measure: abstains per class per day.
// This is a placeholder for the actual implementation that would track this metric.
func TestProbeCountsByClassPerDay(t *testing.T) {
	// The probe must be able to count refusals by class
	// For now, we verify that the probe can read a history with counts
	historyPath := filepath.Join(t.TempDir(), "abstain-history.tsv")
	content := "no-result\t26\nwall refusal\t1\nidle kill\t1\nadmission\t1\nline1-mismatch\t1\n"
	if err := os.WriteFile(historyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	
	learned, err := learnedClasses(historyPath)
	if err != nil {
		t.Fatalf("failed to read history: %s", err)
	}
	
	// All classes should be learned
	expected := map[string]bool{
		"no-result":       true,
		"wall refusal":    true,
		"idle kill":       true,
		"admission":       true,
		"line1-mismatch":  true,
	}
	
	for class, want := range expected {
		if got := learned[class]; got != want {
			t.Errorf("class %q: learned=%v, want=%v", class, got, want)
		}
	}
}

// TestHandoffRecordSupportsNextDecision is a placeholder for the handoff record test.
// A successor must be able to make the same next decision from the record alone.
func TestHandoffRecordSupportsNextDecision(t *testing.T) {
	// This test would verify that a handoff record contains enough information
	// for a successor to make the same decision. For now, it's a placeholder.
	t.Skip("Not yet implemented - handoff record preservation test")
}

// TestCairnPreservesNextDecision is a placeholder for the cairn compaction test.
// The cairn must carry what the next decision needs, not what the last produced.
func TestCairnPreservesNextDecision(t *testing.T) {
	// This test would verify that cairn compaction preserves decision-critical information.
	t.Skip("Not yet implemented - cairn preservation test")
}

// TestLedgerCountsReadAndProbe verifies that the ledger adds the read and probe cost
// to each card's row, so the probe is never free by construction.
func TestLedgerCountsReadAndProbe(t *testing.T) {
	// This test would verify that ledger entries include read and probe costs.
	// For now, it's a placeholder.
	t.Skip("Not yet implemented - ledger cost tracking test")
}

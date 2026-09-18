package swarm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// Replaying a failed provider call must yield 0 additional provider requests
// and exactly 1 usage row per destination across all runs.
func TestTriageDecideFailedProviderCallReplayAccounting(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	dir := t.TempDir()
	customUsage := filepath.Join(dir, "custom-usage.tsv")

	callCount := 0
	failingDo := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		callCount++
		return nil, decide.Usage{InputTokens: 50, OutputTokens: 12, HasInput: true, HasOutput: true}, errors.New("provider 500 error")
	}

	// Initial run: provider fails, usage recorded with rc=2, retained call marked AllDone.
	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: failingDo,
		UsagePath: customUsage,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("initial run rc = %d, stderr: %s", rc, errOut.String())
	}
	if callCount != 1 {
		t.Fatalf("initial run callCount = %d, want 1", callCount)
	}

	// Verify retained call is marked AllDone and Failed on disk.
	retainedPath := p.Path(Usage, fmt.Sprintf("%s-1.decide.json", id))
	rawRetained, err := os.ReadFile(retainedPath)
	if err != nil {
		t.Fatalf("retained call file missing: %v", err)
	}
	var retained retainedDecideCall
	if err := json.Unmarshal(rawRetained, &retained); err != nil {
		t.Fatalf("unmarshal retained call: %v", err)
	}
	if !retained.Failed {
		t.Errorf("retained call Failed = false, want true")
	}
	if !retained.AllDone {
		t.Errorf("retained call AllDone = false, want true")
	}

	for _, dst := range []string{customUsage, p.Path("usage.tsv")} {
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("destination %s missing: %v", dst, err)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("destination %s want 2 lines (header + 1 row), got %d:\n%s", dst, len(lines), string(raw))
		}
		if !strings.Contains(lines[1], "\t2\t") {
			t.Errorf("destination %s row does not have rc=2: %s", dst, lines[1])
		}
	}

	// Replay 1: must make 0 provider calls and append 0 rows.
	out.Reset()
	errOut.Reset()
	rc = Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: failingDo,
		UsagePath: customUsage,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("replay 1 rc = %d, stderr: %s", rc, errOut.String())
	}
	if callCount != 1 {
		t.Fatalf("replay 1 must not call provider: callCount = %d, want 1", callCount)
	}
	for _, dst := range []string{customUsage, p.Path("usage.tsv")} {
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("destination %s missing: %v", dst, err)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("replay 1: destination %s want 2 lines, got %d:\n%s", dst, len(lines), string(raw))
		}
	}

	// Replay 2: must make 0 provider calls and append 0 rows.
	out.Reset()
	errOut.Reset()
	rc = Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: failingDo,
		UsagePath: customUsage,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("replay 2 rc = %d, stderr: %s", rc, errOut.String())
	}
	if callCount != 1 {
		t.Fatalf("replay 2 must not call provider: callCount = %d, want 1", callCount)
	}
	for _, dst := range []string{customUsage, p.Path("usage.tsv")} {
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("destination %s missing: %v", dst, err)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("replay 2: destination %s want 2 lines, got %d:\n%s", dst, len(lines), string(raw))
		}
	}
}

// When destination 1 succeeds and destination 2 fails, a repair run must only
// write destination 2 without duplicating destination 1, producing 1 provider
// call and exactly 1 row in each destination across all runs.
func TestTriageDecidePartialDestinationFailureRecovery(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	sc, ok := p.findSidecar(id)
	if !ok {
		t.Fatalf("sidecar %s not found", id)
	}
	jobUsage := filepath.Join(sc.Job, "usage.tsv")
	poolUsage := p.Path("usage.tsv")

	// Create poolUsage with header initially for upfront validation.
	header := strings.Join(CardUsageColumns, "\t") + "\n"
	if err := os.WriteFile(poolUsage, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}

	callCount := 0
	decideDo := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		callCount++
		// After upfront validation has completed, make poolUsage read-only so AppendCardUsage fails.
		if err := os.Chmod(poolUsage, 0o400); err != nil {
			t.Fatal(err)
		}
		return map[string]decide.Answer{
			"reason":      {Type: "choice", Choice: "provider_error", Probabilities: map[string]float64{"provider_error": 0.95}, Confidence: 0.95},
			"needs_human": {Type: "noul", Noul: 0.10, Confidence: 0.10},
		}, decide.Usage{InputTokens: 88, OutputTokens: 22, HasInput: true, HasOutput: true}, nil
	}

	// Run 1: jobUsage succeeds, poolUsage fails, triage exits 2.
	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: decideDo,
		Stdout: &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 2 {
		t.Fatalf("run 1 rc = %d, want 2 on partial destination failure; stderr: %s", rc, errOut.String())
	}
	if callCount != 1 {
		t.Fatalf("callCount = %d, want 1", callCount)
	}

	// Destination 1 succeeded and has 1 row.
	raw1, err := os.ReadFile(jobUsage)
	if err != nil {
		t.Fatalf("jobUsage must be written: %v", err)
	}
	lines1 := strings.Split(strings.TrimRight(string(raw1), "\n"), "\n")
	if len(lines1) != 2 {
		t.Fatalf("jobUsage want 2 lines (header + 1 row), got %d:\n%s", len(lines1), string(raw1))
	}

	// Destination 2 failed and has 0 usage rows (header only).
	raw2, err := os.ReadFile(poolUsage)
	if err != nil {
		t.Fatalf("poolUsage read error: %v", err)
	}
	lines2 := strings.Split(strings.TrimRight(string(raw2), "\n"), "\n")
	if len(lines2) != 1 {
		t.Fatalf("poolUsage must have only header line after failure, got %d lines: %s", len(lines2), string(raw2))
	}

	// Retained call exists with jobUsage marked completed.
	retainedPath := p.Path(Usage, fmt.Sprintf("%s-1.decide.json", id))
	rawRetained, err := os.ReadFile(retainedPath)
	if err != nil {
		t.Fatalf("retained call file missing: %v", err)
	}
	var retained retainedDecideCall
	if err := json.Unmarshal(rawRetained, &retained); err != nil {
		t.Fatalf("unmarshal retained call: %v", err)
	}
	if !retained.Completed[canonicalUsagePath(jobUsage)] {
		t.Errorf("retained call Completed[%s] want true", jobUsage)
	}
	if retained.Completed[canonicalUsagePath(poolUsage)] {
		t.Errorf("retained call Completed[%s] want false", poolUsage)
	}

	// Repair destination 2.
	if err := os.Chmod(poolUsage, 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 2 (replay): recovers accounting, writes destination 2 only, does not re-append destination 1.
	out.Reset()
	errOut.Reset()
	rc = Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: decideDo,
		Stdout: &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("replay run rc = %d, want 0; stderr: %s", rc, errOut.String())
	}
	if callCount != 1 {
		t.Fatalf("replay run must not call provider: callCount = %d, want 1", callCount)
	}

	// Both destinations now have exactly 1 row.
	raw1After, _ := os.ReadFile(jobUsage)
	lines1After := strings.Split(strings.TrimRight(string(raw1After), "\n"), "\n")
	if len(lines1After) != 2 {
		t.Fatalf("jobUsage after repair want 2 lines (no duplicate), got %d:\n%s", len(lines1After), string(raw1After))
	}

	raw2After, _ := os.ReadFile(poolUsage)
	lines2After := strings.Split(strings.TrimRight(string(raw2After), "\n"), "\n")
	if len(lines2After) != 2 {
		t.Fatalf("poolUsage after repair want 2 lines (header + 1 row), got %d:\n%s", len(lines2After), string(raw2After))
	}
}

// Persisting the retained call must report write errors, bubble up failure,
// and not silently proceed with accounting or decisions.
func TestTriageDecideRetainedCallWriteErrorBubbles(t *testing.T) {
	p, _ := decideTestPool(t, "starting up\nInternal server error\n")
	usageFile := filepath.Join(t.TempDir(), "usage.tsv")

	// Make the usage directory in the pool unwritable so writeAtomic of the retained call fails.
	usageDir := p.Path(Usage)
	if err := os.Chmod(usageDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(usageDir, 0o755)
	})

	callCount := 0
	countingDo := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		callCount++
		return map[string]decide.Answer{
			"reason": {Type: "choice", Choice: "provider_error", Probabilities: map[string]float64{"provider_error": 0.95}, Confidence: 0.95},
		}, decide.Usage{InputTokens: 10, OutputTokens: 5, HasInput: true, HasOutput: true}, nil
	}

	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: countingDo,
		UsagePath: usageFile,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 2 {
		t.Fatalf("triage rc = %d, want 2 when retained call write fails", rc)
	}
	if !strings.Contains(errOut.String(), "TRIAGE REFUSED") || !strings.Contains(errOut.String(), "persist retained call") {
		t.Fatalf("stderr must refuse with retained call error: %s", errOut.String())
	}

	// Must not silently proceed: usage and decisions.log must not be written.
	if _, err := os.Stat(usageFile); !os.IsNotExist(err) {
		t.Fatalf("usage file %s must not be written when retained call write fails", usageFile)
	}
	if _, err := os.Stat(p.Path(DecisionsFile)); !os.IsNotExist(err) {
		t.Fatalf("decisions.log must not be written when retained call write fails")
	}
}

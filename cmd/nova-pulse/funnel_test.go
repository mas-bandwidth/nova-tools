package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSprintFunnelMissingQueueRefused(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"sprint", "funnel"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--queue is required") {
		t.Errorf("refusal missing --queue is required: %s", errb.String())
	}
}

func TestSprintMissingSubverbRefused(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"sprint"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "a sub-verb is required") {
		t.Errorf("refusal missing sub-verb required: %s", errb.String())
	}
}

func TestSprintFunnelCLILifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "queue")

	var out, errb bytes.Buffer
	// 1. Admit card via convenience flag
	code := run([]string{"sprint", "funnel", "--queue", queueDir, "--admit", "card-test-1", "--model", "gemini-2.5-pro"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("admit exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "FUNNEL RECORD event=ADMIT card=card-test-1") {
		t.Errorf("admit output unexpected: %s", out.String())
	}

	// 2. Launch card
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--launch", "card-test-1", "--bench", "studio", "--attempt", "1"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("launch exit = %d, want 0; err=%s", code, errb.String())
	}

	// 3. Harvest card with measured spend
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--harvest", "card-test-1", "--spend", "0.0350", "--verdict", "OK"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("harvest exit = %d, want 0; err=%s", code, errb.String())
	}

	// 4. Gate card
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--gate", "card-test-1", "--verdict", "PASS"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("gate exit = %d, want 0; err=%s", code, errb.String())
	}

	// 5. Land card
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--land", "card-test-1", "--verdict", "MERGED"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("land exit = %d, want 0; err=%s", code, errb.String())
	}

	// 6. Void attempt 1 of another card
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--void-card", "card-test-2", "--attempt", "1", "--detail", "timeout"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("void exit = %d, want 0; err=%s", code, errb.String())
	}

	// 7. Check oneline report
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--oneline"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("oneline report exit = %d, want 0; err=%s", code, errb.String())
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "FUNNEL admit=1 launch=1 harvest=1 gate=1 land=1 void=1 spend=0.0350 cost_per_landed=0.0350") {
		t.Errorf("unexpected oneline report: %s", line)
	}

	// 8. Check JSON report
	out.Reset()
	code = run([]string{"sprint", "funnel", "--queue", queueDir, "--json"},
		&out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("json report exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"landed_cards": 1`) {
		t.Errorf("json report missing landed_cards: %s", out.String())
	}
}

func TestSprintFunnelUnmeasuredSpendStaysDash(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "queue")

	var out, errb bytes.Buffer
	// Admit and Land without spend measurement
	_ = run([]string{"sprint", "funnel", "--queue", queueDir, "--admit", "c1"}, &out, &errb, time.Now().UTC())
	_ = run([]string{"sprint", "funnel", "--queue", queueDir, "--land", "c1"}, &out, &errb, time.Now().UTC())

	out.Reset()
	code := run([]string{"sprint", "funnel", "--queue", queueDir, "--oneline"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	line := strings.TrimSpace(out.String())
	// Invariant: unmeasured spend is "-", NEVER "0" or "0.00"
	if !strings.Contains(line, "spend=-") {
		t.Errorf("expected spend=-, got: %s", line)
	}
	if !strings.Contains(line, "cost_per_landed=-") {
		t.Errorf("expected cost_per_landed=-, got: %s", line)
	}
}

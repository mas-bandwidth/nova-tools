package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDecisions(t *testing.T, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "decisions.jsonl")
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// tonight is tonight's 30 labeled rows: 9 right at 0.93, 21 wrong at 0.40.
func tonight() string {
	var b strings.Builder
	for i := 0; i < 9; i++ {
		b.WriteString(`{"decision":"abstain","confidence":0.93,"label":"abstain"}` + "\n")
	}
	for i := 0; i < 21; i++ {
		b.WriteString(`{"decision":"abstain","confidence":0.40,"label":"needs_human"}` + "\n")
	}
	return b.String()
}

// Rule 8: tune reads the log and prints the per-floor lines and the finish.
func TestTuneVerbTonightShape(t *testing.T) {
	p := writeDecisions(t, tonight())
	var stdout, stderr bytes.Buffer
	code := run([]string{"tune", "--decisions", p}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "TUNE floor=0.9 decided=9 agree=9 agree_rate=1.00 escalated=21 escalation_rate=0.70") {
		t.Fatalf("floor line wrong:\n%s", out)
	}
	if !strings.Contains(out, "TUNE OK lines=30 labeled=30 best_floor=0.9") {
		t.Fatalf("final line wrong:\n%s", out)
	}
}

// Fewer than 10 labeled rows cannot set a floor: exit 2.
func TestTuneVerbExits2UnderTenLabeled(t *testing.T) {
	data := tonight() + `{"decision":"abstain","confidence":0.95}` + "\n"
	// keep only nine labeled rows plus the unlabeled one
	lines := strings.Split(strings.TrimSpace(data), "\n")
	var b strings.Builder
	for i := 0; i < 9; i++ {
		b.WriteString(lines[i] + "\n")
	}
	b.WriteString(`{"decision":"abstain","confidence":0.95}` + "\n")
	p := writeDecisions(t, b.String())
	var stdout, stderr bytes.Buffer
	code := run([]string{"tune", "--decisions", p}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if combined := stdout.String() + stderr.String(); !strings.Contains(combined, "reason=too-few-labeled") {
		t.Fatalf("refusal must say reason=too-few-labeled: %q", combined)
	}
}

// A missing --decisions is a refusal, not a guess.
func TestTuneVerbExits2WithoutDecisions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"tune"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
}

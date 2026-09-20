package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCmdRequeueMovesCardAndWritesRetryMarker(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "pending")
	launched := filepath.Join(dir, "launched")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}

	cardPath := filepath.Join(launched, "card-123.md")
	if err := os.WriteFile(cardPath, []byte("RESULT card-123\nMODEL: opencode/deepseek-v4-flash\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	code := run([]string{"requeue", "--ready", ready, "--launched", launched, "--card", "card-123.md"}, &out, &errs, time.Now().UTC())
	if code != 0 {
		t.Fatalf("requeue exit = %d, want 0; stdout=%s stderr=%s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "REQUEUE OK") || !strings.Contains(out.String(), "attempt=1") {
		t.Fatalf("stdout = %q, want REQUEUE OK attempt=1", out.String())
	}

	// Verify card is now in ready
	if _, err := os.Stat(filepath.Join(ready, "card-123.md")); err != nil {
		t.Fatalf("card not found in ready: %v", err)
	}
	// Verify retry marker in ready
	raw, err := os.ReadFile(filepath.Join(ready, "card-123.md.provider-retry"))
	if err != nil {
		t.Fatalf("missing retry marker: %v", err)
	}
	if !strings.Contains(string(raw), "attempts=1") {
		t.Fatalf("marker content = %q, want attempts=1", string(raw))
	}
}

func TestCmdRequeueExhausted(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "pending")
	launched := filepath.Join(dir, "launched")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}

	cardPath := filepath.Join(launched, "card-456.md")
	if err := os.WriteFile(cardPath, []byte("RESULT card-456\nMODEL: opencode/deepseek-v4-flash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write retry marker with attempts=2
	if err := os.WriteFile(filepath.Join(launched, "card-456.md.provider-retry"), []byte("attempts=2 failed_routes=opencode/deepseek-v4-flash\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	code := run([]string{"requeue", "--ready", ready, "--launched", launched, "--card", "card-456.md", "--max", "2"}, &out, &errs, time.Now().UTC())
	if code != 1 {
		t.Fatalf("requeue exit = %d, want 1 (exhausted); stdout=%s stderr=%s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "REQUEUE EXHAUSTED") {
		t.Fatalf("stdout = %q, want REQUEUE EXHAUSTED", out.String())
	}
	// Card stays in launched
	if _, err := os.Stat(filepath.Join(launched, "card-456.md")); err != nil {
		t.Fatalf("card should remain in launched: %v", err)
	}
	// Provider-failed marker written in launched
	if _, err := os.Stat(filepath.Join(launched, "card-456.md.provider-failed")); err != nil {
		t.Fatalf("missing provider-failed marker: %v", err)
	}
}

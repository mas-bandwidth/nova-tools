package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPoolCapacityCLI tests running `nova-pulse pool-capacity` via CLI flag parsing.
func TestPoolCapacityCLI(t *testing.T) {
	root := t.TempDir()
	slotsDir := filepath.Join(root, "pool", "slots")
	if err := os.MkdirAll(slotsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	slot1 := filepath.Join(slotsDir, "slot-1.json")
	if err := os.WriteFile(slot1, []byte(`{"state":"free"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	capFile := filepath.Join(root, "capacity")
	if err := os.WriteFile(capFile, []byte("cores=4 load1=0.75 mem_gb=16"), 0o644); err != nil {
		t.Fatal(err)
	}

	metricsOut := filepath.Join(t.TempDir(), "metrics.tsv")

	exit, stdout, stderr := invokePulse(t, "pool-capacity", "--roots", root, "--headroom", "120", "--out", metricsOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", exit, stderr)
	}

	if !strings.HasPrefix(stdout, "PULSE POOL-CAPACITY ") {
		t.Errorf("stdout does not start with PULSE POOL-CAPACITY: %q", stdout)
	}

	for _, want := range []string{"benches=1", "slots=1", "memory=16", "load=0.75", "in_flight=0", "headroom=120"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q: %q", want, stdout)
		}
	}

	data, err := os.ReadFile(metricsOut)
	if err != nil {
		t.Fatalf("reading %s: %v", metricsOut, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 row in metrics.tsv, got %d", len(lines))
	}
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 6 {
		t.Fatalf("expected 6 columns in metrics row, got %d: %q", len(fields), lines[0])
	}
	if fields[1] != "1" || fields[2] != "16" || fields[3] != "0.75" || fields[4] != "0" || fields[5] != "120" {
		t.Errorf("unexpected metrics fields: %v", fields)
	}
}

// TestPoolCapacityCLISubcommandPoolCapacity verifies that `nova-pulse pool capacity` dispatches to pool-capacity.
func TestPoolCapacityCLISubcommandPoolCapacity(t *testing.T) {
	root := t.TempDir()
	slotsDir := filepath.Join(root, "pool", "slots")
	if err := os.MkdirAll(slotsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(slotsDir, "slot-1.json"), []byte(`{"state":"free"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "capacity"), []byte("cores=8 load1=1.0 mem_gb=32"), 0o644); err != nil {
		t.Fatal(err)
	}

	metricsOut := filepath.Join(t.TempDir(), "metrics.tsv")

	exit, stdout, stderr := invokePulse(t, "pool", "capacity", "--roots", root, "--headroom", "45", "--out", metricsOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", exit, stderr)
	}

	if !strings.HasPrefix(stdout, "PULSE POOL-CAPACITY ") {
		t.Errorf("stdout does not start with PULSE POOL-CAPACITY: %q", stdout)
	}
	if !strings.Contains(stdout, "headroom=45") {
		t.Errorf("expected headroom=45 in stdout: %q", stdout)
	}
}

// TestPoolCapacityCLIAppendMetrics verifies that multiple runs append rows atomically.
func TestPoolCapacityCLIAppendMetrics(t *testing.T) {
	root := t.TempDir()
	slotsDir := filepath.Join(root, "pool", "slots")
	if err := os.MkdirAll(slotsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotsDir, "slot-1.json"), []byte(`{"state":"free"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	metricsOut := filepath.Join(t.TempDir(), "metrics.tsv")

	// First run
	exit1, _, stderr1 := invokePulse(t, "pool-capacity", "--roots", root, "--headroom", "10", "--out", metricsOut)
	if exit1 != 0 {
		t.Fatalf("run 1 failed: %d, stderr: %s", exit1, stderr1)
	}

	// Second run (append)
	exit2, _, stderr2 := invokePulse(t, "pool-capacity", "--roots", root, "--headroom", "20", "--out", metricsOut)
	if exit2 != 0 {
		t.Fatalf("run 2 failed: %d, stderr: %s", exit2, stderr2)
	}

	data, err := os.ReadFile(metricsOut)
	if err != nil {
		t.Fatalf("reading metrics file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows appended in metrics.tsv, got %d: %q", len(lines), string(data))
	}
}

// TestPoolCapacityCLIOverwriteFlag verifies that --overwrite replaces metrics.tsv.
func TestPoolCapacityCLIOverwriteFlag(t *testing.T) {
	root := t.TempDir()
	slotsDir := filepath.Join(root, "pool", "slots")
	if err := os.MkdirAll(slotsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotsDir, "slot-1.json"), []byte(`{"state":"free"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	metricsOut := filepath.Join(t.TempDir(), "metrics.tsv")

	// First run
	if exit, _, stderr := invokePulse(t, "pool-capacity", "--roots", root, "--headroom", "10", "--out", metricsOut); exit != 0 {
		t.Fatalf("run 1 failed: %d, stderr: %s", exit, stderr)
	}

	// Second run with --overwrite
	if exit, _, stderr := invokePulse(t, "pool-capacity", "--roots", root, "--headroom", "20", "--overwrite", "--out", metricsOut); exit != 0 {
		t.Fatalf("run 2 failed: %d, stderr: %s", exit, stderr)
	}

	data, err := os.ReadFile(metricsOut)
	if err != nil {
		t.Fatalf("reading metrics file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 row after overwrite, got %d: %q", len(lines), string(data))
	}
	if !strings.HasSuffix(lines[0], "\t20") {
		t.Errorf("expected row to end with headroom 20: %q", lines[0])
	}
}

// TestPoolCapacityCLIRefusals verifies that bad arguments produce exit 2 and helpful stderr.
func TestPoolCapacityCLIRefusals(t *testing.T) {
	// No benches
	exit, _, stderr := invokePulse(t, "pool-capacity")
	if exit != 2 {
		t.Fatalf("expected exit 2, got %d", exit)
	}
	if !strings.Contains(stderr, "POOL-CAPACITY REFUSED") {
		t.Errorf("stderr missing POOL-CAPACITY REFUSED: %q", stderr)
	}
}

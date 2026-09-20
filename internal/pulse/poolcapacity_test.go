package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPoolCapacityAggregate verifies aggregation of slots, memory, load, in-flight jobs,
// and provider rate-limit headroom across all benches.
func TestPoolCapacityAggregate(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	benches := []BenchCapacity{
		{
			Name:       "bench-alpha",
			Slots:      8,
			SlotsTotal: 8,
			SlotsUsed:  3,
			SlotsFree:  5,
			MemoryGB:   32.0,
			Load:       1.50,
			Cores:      8,
			InFlight:   3,
		},
		{
			Name:       "bench-beta",
			Slots:      16,
			SlotsTotal: 16,
			SlotsUsed:  4,
			SlotsFree:  12,
			MemoryGB:   64.0,
			Load:       2.50,
			Cores:      16,
			InFlight:   4,
		},
		{
			Name:       "bench-gamma",
			Slots:      4,
			SlotsTotal: 4,
			SlotsUsed:  0,
			SlotsFree:  4,
			MemoryGB:   16.0,
			Load:       0.50,
			Cores:      4,
			InFlight:   0,
		},
	}

	summary := AggregatePoolCapacity(benches, 150, now)

	if summary.Benches != 3 {
		t.Errorf("expected 3 benches, got %d", summary.Benches)
	}
	if summary.SlotsTotal != 28 {
		t.Errorf("expected 28 total slots, got %d", summary.SlotsTotal)
	}
	if summary.SlotsUsed != 7 {
		t.Errorf("expected 7 used slots, got %d", summary.SlotsUsed)
	}
	if summary.SlotsFree != 21 {
		t.Errorf("expected 21 free slots, got %d", summary.SlotsFree)
	}
	if summary.Slots != 28 {
		t.Errorf("expected 28 slots, got %d", summary.Slots)
	}
	if summary.MemoryGB != 112.0 {
		t.Errorf("expected 112.0 GB memory, got %f", summary.MemoryGB)
	}
	if summary.Load != 4.50 {
		t.Errorf("expected 4.50 total load, got %f", summary.Load)
	}
	if summary.LoadAvg != 1.50 {
		t.Errorf("expected 1.50 average load, got %f", summary.LoadAvg)
	}
	if summary.InFlight != 7 {
		t.Errorf("expected 7 in-flight jobs, got %d", summary.InFlight)
	}
	if summary.ProviderHeadroom != 150 {
		t.Errorf("expected 150 provider headroom, got %d", summary.ProviderHeadroom)
	}
	if !summary.Timestamp.Equal(now) {
		t.Errorf("expected timestamp %v, got %v", now, summary.Timestamp)
	}
}

// TestPoolCapacityFormatMetricsRow verifies the 6-column TSV format:
// <RFC3339>\t<slots>\t<memory>\t<load>\t<in_flight>\t<headroom>\n
func TestPoolCapacityFormatMetricsRow(t *testing.T) {
	now := time.Date(2026, 9, 20, 15, 30, 0, 0, time.UTC)
	summary := PoolCapacitySummary{
		Timestamp:        now,
		Benches:          2,
		Slots:            24,
		SlotsTotal:       24,
		SlotsUsed:        4,
		SlotsFree:        20,
		MemoryGB:         128.0,
		Load:             3.20,
		InFlight:         4,
		ProviderHeadroom: 200,
	}

	row := FormatMetricsRow(summary)
	fields := strings.Split(strings.TrimSuffix(row, "\n"), "\t")
	if len(fields) != 6 {
		t.Fatalf("expected 6 fields in metrics row, got %d: %q", len(fields), row)
	}

	if fields[0] != "2026-09-20T15:30:00Z" {
		t.Errorf("field 0 timestamp = %q, want 2026-09-20T15:30:00Z", fields[0])
	}
	if fields[1] != "24" {
		t.Errorf("field 1 slots = %q, want 24", fields[1])
	}
	if fields[2] != "128" {
		t.Errorf("field 2 memory = %q, want 128", fields[2])
	}
	if fields[3] != "3.20" {
		t.Errorf("field 3 load = %q, want 3.20", fields[3])
	}
	if fields[4] != "4" {
		t.Errorf("field 4 in_flight = %q, want 4", fields[4])
	}
	if fields[5] != "200" {
		t.Errorf("field 5 headroom = %q, want 200", fields[5])
	}
}

// TestPoolCapacityAtomicMetricsTSV tests atomic creation, appending, and overwriting of metrics.tsv.
func TestPoolCapacityAtomicMetricsTSV(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.tsv")

	row1 := "2026-09-20T10:00:00Z\t10\t64\t1.20\t2\t50\n"
	if err := WriteMetricsTSVAtomic(metricsPath, row1, true); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	content, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("reading metrics.tsv: %v", err)
	}
	if string(content) != row1 {
		t.Fatalf("expected %q, got %q", row1, string(content))
	}

	row2 := "2026-09-20T11:00:00Z\t12\t96\t1.80\t3\t60\n"
	if err := WriteMetricsTSVAtomic(metricsPath, row2, true); err != nil {
		t.Fatalf("append write failed: %v", err)
	}

	content2, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("reading metrics.tsv after append: %v", err)
	}
	expected := row1 + row2
	if string(content2) != expected {
		t.Fatalf("expected %q, got %q", expected, string(content2))
	}

	// Test overwrite mode
	row3 := "2026-09-20T12:00:00Z\t14\t128\t2.00\t4\t70\n"
	if err := WriteMetricsTSVAtomic(metricsPath, row3, false); err != nil {
		t.Fatalf("overwrite write failed: %v", err)
	}

	content3, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("reading metrics.tsv after overwrite: %v", err)
	}
	if string(content3) != row3 {
		t.Fatalf("expected %q after overwrite, got %q", row3, string(content3))
	}
}

// TestPoolCapacityAtomicConcurrencyOExcl verifies that concurrent writers appending
// to metrics.tsv via O_EXCL tempfile and rename do not corrupt the file.
func TestPoolCapacityAtomicConcurrencyOExcl(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.tsv")

	const numWriters = 20
	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(idx int) {
			defer wg.Done()
			row := fmt.Sprintf("2026-09-20T12:%02d:00Z\t%d\t64\t1.00\t0\t50\n", idx, idx)
			// Retrying with exponential backoff if rename collides
			for attempt := 0; attempt < 10; attempt++ {
				if err := WriteMetricsTSVAtomic(metricsPath, row, true); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	content, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("reading metrics.tsv: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) == 0 {
		t.Fatal("expected non-empty metrics.tsv")
	}
	for _, l := range lines {
		fields := strings.Split(l, "\t")
		if len(fields) != 6 {
			t.Errorf("corrupt row with %d fields: %q", len(fields), l)
		}
	}
}

// TestPoolCapacityRefusals verifies proper exit code 2 and error reporting on invalid inputs.
func TestPoolCapacityRefusals(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// No benches provided
	code := PoolCapacity(PoolCapacityInput{
		MetricsPath: filepath.Join(t.TempDir(), "metrics.tsv"),
		Stdout:      &stdout,
		Stderr:      &stderr,
	})
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "POOL-CAPACITY REFUSED: no benches specified") {
		t.Errorf("unexpected refusal: %s", stderr.String())
	}

	// Invalid provider file
	stderr.Reset()
	code = PoolCapacity(PoolCapacityInput{
		Benches:       []BenchCapacity{{Name: "b1", Slots: 2}},
		ProvidersPath: "/nonexistent/providers.tsv",
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if code != 2 {
		t.Errorf("expected exit code 2 for bad providers file, got %d", code)
	}
	if !strings.Contains(stderr.String(), "POOL-CAPACITY REFUSED providers=") {
		t.Errorf("unexpected refusal: %s", stderr.String())
	}
}

// TestPoolCapacityWithRootsAndQueue tests reading slot files and in-flight queue cards from disk.
func TestPoolCapacityWithRootsAndQueue(t *testing.T) {
	root := t.TempDir()
	slotsDir := filepath.Join(root, "pool", "slots")
	if err := os.MkdirAll(slotsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write 2 slots: one free, one held
	slot1 := filepath.Join(slotsDir, "slot-1.json")
	slot2 := filepath.Join(slotsDir, "slot-2.json")
	if err := os.WriteFile(slot1, []byte(`{"state":"free"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slot2, []byte(`{"state":"running","pid":1234}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write capacity file
	capFile := filepath.Join(root, "capacity")
	if err := os.WriteFile(capFile, []byte("cores=8 load1=1.45 mem_gb=32"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Queue with 1 launched card
	queueDir := t.TempDir()
	launchedDir := filepath.Join(queueDir, "launched")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launchedDir, "card-101.md"), []byte("# Card 101"), 0o644); err != nil {
		t.Fatal(err)
	}

	metricsOut := filepath.Join(t.TempDir(), "metrics.tsv")
	var stdout, stderr bytes.Buffer

	code := PoolCapacity(PoolCapacityInput{
		Roots:         root,
		QueueDir:      queueDir,
		MetricsPath:   metricsOut,
		Headroom:      80,
		AppendMetrics: true,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})

	if code != 0 {
		t.Fatalf("PoolCapacity failed with code %d; stderr=%s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.HasPrefix(out, "PULSE POOL-CAPACITY ") {
		t.Errorf("stdout does not start with PULSE POOL-CAPACITY: %q", out)
	}
	for _, want := range []string{"benches=1", "slots=2", "memory=32", "load=1.45", "in_flight=1", "headroom=80", "metrics="} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q: %q", want, out)
		}
	}

	rawMetrics, err := os.ReadFile(metricsOut)
	if err != nil {
		t.Fatalf("reading metrics output: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(rawMetrics)), "\t")
	if len(fields) != 6 {
		t.Fatalf("metrics row has %d fields, want 6: %q", len(fields), string(rawMetrics))
	}
	if fields[1] != "2" {
		t.Errorf("metrics slots = %q, want 2", fields[1])
	}
	if fields[2] != "32" {
		t.Errorf("metrics memory = %q, want 32", fields[2])
	}
	if fields[3] != "1.45" {
		t.Errorf("metrics load = %q, want 1.45", fields[3])
	}
	if fields[4] != "1" {
		t.Errorf("metrics in_flight = %q, want 1", fields[4])
	}
	if fields[5] != "80" {
		t.Errorf("metrics headroom = %q, want 80", fields[5])
	}
}

// TestPoolCapacityWithProvidersFile verifies provider rate-limit headroom calculation from TSV.
func TestPoolCapacityWithProvidersFile(t *testing.T) {
	provFile := filepath.Join(t.TempDir(), "providers.tsv")
	content := strings.Join([]string{
		"# route\tprovider\tmodel\ttier\tconcurrency\tcost\tthreshold\tnotes",
		"deepseek-direct\tdeepseek\tdeepseek-v4-flash\tflash\t25\t0.14\t0.20\tprimary",
		"opencode-deepseek\topencode\tdeepseek-v4-flash\tflash\t15\t0.15\t0.20\tbackup",
		"mercury-pro\tmercury\topencode-2.5-pro\tpro\t10\t1.25\t0.15\tpro tier",
	}, "\n")
	if err := os.WriteFile(provFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	headroom, err := ReadProvidersHeadroom(provFile)
	if err != nil {
		t.Fatalf("ReadProvidersHeadroom failed: %v", err)
	}
	// Total concurrency = 25 + 15 + 10 = 50
	if headroom != 50 {
		t.Errorf("expected headroom 50, got %d", headroom)
	}

	metricsOut := filepath.Join(t.TempDir(), "metrics.tsv")
	var stdout, stderr bytes.Buffer
	code := PoolCapacity(PoolCapacityInput{
		Benches: []BenchCapacity{
			{Name: "bench1", Slots: 4, MemoryGB: 16, Load: 0.8},
		},
		ProvidersPath: provFile,
		Headroom:      -1, // use provider file
		MetricsPath:   metricsOut,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if code != 0 {
		t.Fatalf("PoolCapacity failed: %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "headroom=50") {
		t.Errorf("expected headroom=50 in stdout, got %q", stdout.String())
	}
}

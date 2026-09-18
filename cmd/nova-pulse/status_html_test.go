package main

// status --html is the fleet status page as a verb (status-page.sh folded in). The live
// count is the number of running card processes per bench -- the authoritative-liveness
// shape bench-hygiene.sh's live_slot uses -- never the old log-age test, which said a
// silently dead card was alive and a long card was dead. The reader is injected so no test
// starts ssh, and both the page and the metrics.tsv row carry counts only: no card id,
// branch name or label ever reaches a public page.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// testFleetReader pins the seam: two benches in file order, with the process counts and
// disk/mem the page and the metrics row fold.
func testFleetReader(t *testing.T, want ...pulse.BenchReading) pulse.FleetReader {
	t.Helper()
	return func(benches []pulse.FleetBench, now time.Time) []pulse.BenchReading {
		if len(benches) != len(want) {
			t.Fatalf("reader got %d benches, want %d: %+v", len(benches), len(want), benches)
		}
		for i := range want {
			if benches[i].Name != want[i].Name {
				t.Fatalf("reader got bench %d %q, want %q", i, benches[i].Name, want[i].Name)
			}
		}
		return want
	}
}

func withFleetReader(t *testing.T, r pulse.FleetReader) {
	t.Helper()
	old := statusHTMLReader
	statusHTMLReader = r
	t.Cleanup(func() { statusHTMLReader = old })
}

// status-html-writes-the-page-and-one-metrics-row: the page carries the per-bench numbers
// read from running card processes, the metrics.tsv row beside it has the seven columns,
// and a card id in the queue never leaks onto the page.
func TestStatusHTMLWritesPageAndMetricsFromRunningProcesses(t *testing.T) {
	withFleetReader(t, testFleetReader(t,
		pulse.BenchReading{Name: "alpha", Live: 3, Cores: 8, Load: 1, FreeGB: 80, MemGB: 60, Allowed: 5},
		pulse.BenchReading{Name: "beta", Live: 1, Cores: 4, Load: 2, FreeGB: 40, MemGB: 20, Allowed: 2},
	))

	queue := t.TempDir()
	write(t, filepath.Join(queue, "pending", "card-1234.md"), "RESULT: card-1234 must never reach the page\n")
	write(t, filepath.Join(queue, "fill.log"), "attempt=card-1\nattempt=card-2\n")

	benches := filepath.Join(t.TempDir(), "benches.tsv")
	write(t, benches, "alpha\tfake-alpha\t/tmp/alpha\t-\nbeta\tfake-beta\t/tmp/beta\t-\n")

	dir := t.TempDir()
	out := filepath.Join(dir, "index.html")
	exit, stdout, stderr := invokePulse(t, "status", "--queue", queue, "--benches", benches, "--html", out)
	if exit != 0 {
		t.Fatalf("status --html exit = %d, want 0; stderr=%s", exit, stderr)
	}
	// down= counts the benches that did not answer: a fleet partly unseen is not a fleet
	// with nothing to do, and the one line says which of the two this is.
	if want := "STATUS HTML wrote=" + out + " live=4 queue=1 down=0\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}

	html, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the page was not written: %v", err)
	}
	for _, want := range []string{
		"<td>alpha</td><td>3</td><td>8</td><td>1</td><td>80 GB</td><td>60 GB</td><td>5</td>",
		"<td>beta</td><td>1</td><td>4</td><td>2</td><td>40 GB</td><td>20 GB</td><td>2</td>",
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("the page does not carry the bench row %q:\n%s", want, html)
		}
	}
	if strings.Contains(string(html), "card-") {
		t.Errorf("the page carries a card id:\n%s", html)
	}

	metrics, err := os.ReadFile(filepath.Join(dir, "metrics.tsv"))
	if err != nil {
		t.Fatalf("the metrics row was not written beside the page: %v", err)
	}
	line := strings.TrimSuffix(string(metrics), "\n")
	fields := strings.Split(line, "\t")
	if len(fields) != 7 {
		t.Fatalf("the metrics row has %d fields, want 7: %q", len(fields), line)
	}
	for i, want := range []string{"2026-09-16T12:00:00Z", "4", "1", "0", "0", "2", "80,40"} {
		if fields[i] != want {
			t.Errorf("metrics field %d = %q, want %q (row %q)", i, fields[i], want, line)
		}
	}
}

// status-html-appends-the-metrics-row: the verb edits the time series, it never rewrites it.
func TestStatusHTMLAppendsMetricsRow(t *testing.T) {
	withFleetReader(t, testFleetReader(t,
		pulse.BenchReading{Name: "alpha", Live: 2, Cores: 1, Load: 0, FreeGB: 50, MemGB: 10, Allowed: 1},
	))
	queue := t.TempDir()
	benches := filepath.Join(t.TempDir(), "benches.tsv")
	write(t, benches, "alpha\tfake-alpha\t/tmp/alpha\t-\n")
	dir := t.TempDir()
	out := filepath.Join(dir, "index.html")
	for i := 0; i < 2; i++ {
		if exit, _, stderr := invokePulse(t, "status", "--queue", queue, "--benches", benches, "--html", out); exit != 0 {
			t.Fatalf("run %d exit = %d; stderr=%s", i, exit, stderr)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "metrics.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("metrics.tsv has %d rows, want 2 appended:\n%s", len(lines), raw)
	}
	for _, l := range lines {
		if n := len(strings.Split(l, "\t")); n != 7 {
			t.Errorf("row %q has %d fields, want 7", l, n)
		}
	}
}

// status-html-refuses-with-one-remedy-line: --html without a fleet is refused, not guessed.
func TestStatusHTMLRefusesWithoutBenches(t *testing.T) {
	exit, _, stderr := invokePulse(t, "status", "--queue", t.TempDir(), "--html", filepath.Join(t.TempDir(), "index.html"))
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "--benches") {
		t.Errorf("the refusal does not name --benches:\n%s", stderr)
	}
	if got := strings.Count(strings.TrimSpace(stderr), "\n"); got != 0 {
		t.Errorf("the refusal is %d lines, want one remedy line:\n%s", got+1, stderr)
	}
}

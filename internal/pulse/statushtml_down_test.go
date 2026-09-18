package pulse

// The one thing status-page.sh got right that the verb did not. The script's own comment
// records it from the 2026-09-17 pit stop: "a bench that does not answer says DOWN: a row
// of zeros read as an idle bench". The Go page rendered an unreachable bench as zero live,
// zero load, zero free disk -- which a reader takes for a quiet bench with nothing to do,
// exactly when the truth is that nobody can see it at all.
//
// And the page the fleet actually reads carries the time series: status-page.sh drew two
// charts from metrics.tsv, the verb wrote metrics.tsv beside the page and drew nothing.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runStatusHTML(t *testing.T, dir, benches, queue string, readings []BenchReading) (string, string, int) {
	t.Helper()
	now, _ := time.Parse(time.RFC3339, "2026-09-16T12:00:00Z")
	var out, errs bytes.Buffer
	code := StatusHTML(StatusHTMLInput{
		HTML:    filepath.Join(dir, "index.html"),
		Benches: benches,
		Queue:   queue,
		Reader:  func([]FleetBench, time.Time) []BenchReading { return readings },
		Now:     func() time.Time { return now },
		Stdout:  &out,
		Stderr:  &errs,
	})
	return out.String(), errs.String(), code
}

func writeBenchesFile(t *testing.T, names ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "benches.tsv")
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "\tfake-" + n + "\t/tmp/" + n + "\t-\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// status-html-a-silent-bench-says-down: the row says DOWN rather than a row of zeros, and
// the STATUS HTML line counts it, so a reader is told the fleet is partly unseen.
func TestStatusHTMLSilentBenchSaysDown(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runStatusHTML(t, dir, writeBenchesFile(t, "alpha", "beta"), t.TempDir(),
		[]BenchReading{
			{Name: "alpha", Live: 3, Cores: 8, Load: 1, FreeGB: 80, MemGB: 60, Allowed: 5},
			{Name: "beta", Down: true},
		})
	if code != 0 {
		t.Fatalf("status --html exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "down=1") {
		t.Errorf("the STATUS HTML line does not count the silent bench:\n%s", stdout)
	}
	html := readFileString(t, filepath.Join(dir, "index.html"))
	if !strings.Contains(html, "<td>beta</td>") || !strings.Contains(html, "<b>DOWN</b>") {
		t.Errorf("the page does not say beta is DOWN:\n%s", html)
	}
	if strings.Contains(html, "<td>beta</td><td>0</td>") {
		t.Errorf("the silent bench still renders as a row of zeros (an idle bench):\n%s", html)
	}
}

// A bench that answers is untouched by the DOWN path, and down=0 is the healthy fleet.
func TestStatusHTMLEveryBenchAnsweringSaysDownZero(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runStatusHTML(t, dir, writeBenchesFile(t, "alpha"), t.TempDir(),
		[]BenchReading{{Name: "alpha", Live: 2, Cores: 4, Load: 1, FreeGB: 50, MemGB: 20, Allowed: 3}})
	if code != 0 {
		t.Fatalf("status --html exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "down=0") {
		t.Errorf("the STATUS HTML line does not say down=0:\n%s", stdout)
	}
	html := readFileString(t, filepath.Join(dir, "index.html"))
	if !strings.Contains(html, "<td>alpha</td><td>2</td><td>4</td><td>1</td><td>50 GB</td><td>20 GB</td><td>3</td>") {
		t.Errorf("the healthy bench row changed:\n%s", html)
	}
	// The legend explains what DOWN means on every page; no ROW may say it here.
	if strings.Contains(html, "<b>DOWN</b>") {
		t.Errorf("a healthy fleet has a DOWN row:\n%s", html)
	}
}

// A silent bench's free disk is not zero, it is unknown: the metrics row must not claim a
// number the fleet never gave, and a dash is how the time series says "no answer".
func TestStatusHTMLMetricsRowSaysDashForASilentBench(t *testing.T) {
	dir := t.TempDir()
	if _, stderr, code := runStatusHTML(t, dir, writeBenchesFile(t, "alpha", "beta"), t.TempDir(),
		[]BenchReading{
			{Name: "alpha", Live: 1, Cores: 4, Load: 0, FreeGB: 90, MemGB: 30, Allowed: 2},
			{Name: "beta", Down: true},
		}); code != 0 {
		t.Fatalf("status --html exit = %d; stderr=%s", code, stderr)
	}
	row := strings.TrimSuffix(readFileString(t, filepath.Join(dir, "metrics.tsv")), "\n")
	fields := strings.Split(row, "\t")
	if len(fields) != 7 {
		t.Fatalf("the metrics row has %d fields, want 7: %q", len(fields), row)
	}
	if fields[6] != "90,-" {
		t.Fatalf("free disk = %q, want %q (a silent bench is a dash, never a zero)", fields[6], "90,-")
	}
}

// status-html-carries-the-time-series: the page draws metrics.tsv, the file it writes
// beside itself. status-page.sh's two charts are what a reader watches to see the fleet
// widen or stall, and a page that writes the series and draws nothing loses them.
func TestStatusHTMLPageDrawsTheTimeSeries(t *testing.T) {
	dir := t.TempDir()
	if _, stderr, code := runStatusHTML(t, dir, writeBenchesFile(t, "alpha"), t.TempDir(),
		[]BenchReading{{Name: "alpha", Live: 1, Cores: 4, Load: 0, FreeGB: 90, MemGB: 30, Allowed: 2}}); code != 0 {
		t.Fatalf("status --html exit = %d; stderr=%s", code, stderr)
	}
	html := readFileString(t, filepath.Join(dir, "index.html"))
	for _, want := range []string{"<canvas", "metrics.tsv", "live cards", "queue depth"} {
		if !strings.Contains(html, want) {
			t.Errorf("the page does not draw the time series (%q missing):\n%s", want, html)
		}
	}
}

// The page is still counts only: no card id, branch name or label reaches it, DOWN row or
// not. A public page carrying a card label is the one failure this page cannot have.
func TestStatusHTMLPageStaysCountsOnly(t *testing.T) {
	queue := t.TempDir()
	writeStatusFile(t, queue, filepath.Join("pending", "card-4242.md"), "RESULT: card-4242 stays off the page\n")
	dir := t.TempDir()
	if _, stderr, code := runStatusHTML(t, dir, writeBenchesFile(t, "alpha", "beta"), queue,
		[]BenchReading{
			{Name: "alpha", Live: 1, Cores: 4, Load: 0, FreeGB: 90, MemGB: 30, Allowed: 2},
			{Name: "beta", Down: true},
		}); code != 0 {
		t.Fatalf("status --html exit = %d; stderr=%s", code, stderr)
	}
	if html := readFileString(t, filepath.Join(dir, "index.html")); strings.Contains(html, "card-") {
		t.Fatalf("the page carries a card id:\n%s", html)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

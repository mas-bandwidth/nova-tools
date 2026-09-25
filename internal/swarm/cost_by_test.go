package swarm

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Card 8358: `cost --by model|day|repo` sums the pooled usage per group. Five rows
// across two models and two days, one row with no USD, so the grouping has something
// to sum and something to propagate.

type costByRow struct {
	id                      string
	day, model, repo        string
	in, out, cw, cr, reason int
	usd                     string
}

func costByPool(t *testing.T) *Pool {
	t.Helper()
	p, err := OpenPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := []costByRow{
		{"20260913T000001Z-a1", "2026-09-13", "model-a", "repo-x", 10, 1, 2, 100, 3, "1.0000"},
		{"20260913T000002Z-a2", "2026-09-13", "model-a", "repo-y", 20, 2, 3, 200, 4, "2.0000"},
		{"20260913T000003Z-b1", "2026-09-13", "model-b", "repo-x", 30, 3, 4, 50, 5, "0.5000"},
		{"20260914T000004Z-a3", "2026-09-14", "model-a", "repo-x", 40, 4, 5, 10, 6, "4.0000"},
		{"20260914T000005Z-b2", "2026-09-14", "model-b", "repo-y", 50, 5, 6, 400, 7, Dash},
	}
	for _, r := range rows {
		row := UsageRow{
			"job": r.id, "attempt": "1",
			"started": r.day + "T00:00:00Z", "ended": r.day + "T00:00:01Z",
			"end": EndDone, "model": r.model, "repo": r.repo,
			"tokens_in": fmt.Sprint(r.in), "tokens_out": fmt.Sprint(r.out),
			"cache_write": fmt.Sprint(r.cw), "cache_read": fmt.Sprint(r.cr),
			"reasoning": fmt.Sprint(r.reason), "usd": r.usd,
		}
		if _, _, err := p.WriteUsage(r.id, row); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func costByLines(t *testing.T, out, by string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "COST BY "+by+"=") {
			got = append(got, line)
		}
	}
	return got
}

func runCost(t *testing.T, by string, summaryOnly bool) string {
	t.Helper()
	p := costByPool(t)
	var stdout, stderr bytes.Buffer
	if exit := Cost(p, "", 0, by, summaryOnly, &stdout, &stderr); exit != 0 {
		t.Fatalf("Cost exited %d: %s", exit, stderr.String())
	}
	return stdout.String()
}

func TestCostByModelSumsEachModel(t *testing.T) {
	got := costByLines(t, runCost(t, "model", false), "model")
	want := []string{
		"COST BY model=model-b tasks=2 in=80 out=8 cache_write=10 cache_read=450 reasoning=12 usd=-",
		"COST BY model=model-a tasks=3 in=70 out=7 cache_write=10 cache_read=310 reasoning=13 usd=7.0000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("by model lines:\n got %q\nwant %q", got, want)
	}
}

func TestCostByDaySumsEachDay(t *testing.T) {
	got := costByLines(t, runCost(t, "day", false), "day")
	want := []string{
		"COST BY day=2026-09-14 tasks=2 in=90 out=9 cache_write=11 cache_read=410 reasoning=13 usd=-",
		"COST BY day=2026-09-13 tasks=3 in=60 out=6 cache_write=9 cache_read=350 reasoning=12 usd=3.5000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("by day lines:\n got %q\nwant %q", got, want)
	}
}

func TestCostByDashUSDPropagates(t *testing.T) {
	got := costByLines(t, runCost(t, "repo", false), "repo")
	want := []string{
		"COST BY repo=repo-y tasks=2 in=70 out=7 cache_write=9 cache_read=600 reasoning=11 usd=-",
		"COST BY repo=repo-x tasks=3 in=80 out=8 cache_write=11 cache_read=160 reasoning=14 usd=5.5000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("by repo lines:\n got %q\nwant %q", got, want)
	}
}

func TestCostBySitsBetweenTaskLinesAndSummary(t *testing.T) {
	out := runCost(t, "model", false)
	lastTask := strings.LastIndex(out, "COST TASK ")
	firstBy := strings.Index(out, "COST BY model=")
	summary := strings.Index(out, "COST OK ")
	if lastTask < 0 || firstBy < 0 || summary < 0 {
		t.Fatalf("missing task, group or summary lines:\n%s", out)
	}
	if lastTask > firstBy || firstBy > summary {
		t.Fatalf("COST BY is not between the task lines and COST OK:\n%s", out)
	}
}

func TestCostSummaryOnlySkipsTaskLines(t *testing.T) {
	out := runCost(t, "model", true)
	if strings.Contains(out, "COST TASK") || strings.Contains(out, "COST MORE") {
		t.Fatalf("summary-only kept per-task lines:\n%s", out)
	}
	if !strings.Contains(out, "COST BY model=model-a") || !strings.Contains(out, "COST OK ") {
		t.Fatalf("summary-only dropped the group or summary:\n%s", out)
	}
}

func TestCostByRefusesAnUnknownGroup(t *testing.T) {
	p := costByPool(t)
	var stdout, stderr bytes.Buffer
	if exit := Cost(p, "", 0, "colour", false, &stdout, &stderr); exit != 2 {
		t.Fatalf("Cost exited %d, want 2; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "COST REFUSED") {
		t.Fatalf("refusal did not name itself: %s", stderr.String())
	}
}

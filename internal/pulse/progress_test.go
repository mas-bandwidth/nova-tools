package pulse

// The progress verb's acceptance replays (SPEC-PULSE.md, "Progress"):
// progress-counts-only-the-day, progress-parallelism-is-busy-over-span,
// estimate-uses-p90-and-parallelism. Every gh is a fixture on PATH; the queue
// and the usage.tsv rows are files a test writes under t.TempDir.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeProgressUsage writes one job's usage.tsv in the thirteen-column shape the
// native run writes (internal/swarm CardUsageColumns), carrying the columns the
// progress verb reads: started, ended, rc, usd.
func writeProgressUsage(t *testing.T, root, slot, job, started, ended, rc, usd string) {
	t.Helper()
	dir := filepath.Join(root, slot, "jobs", job)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n" +
		job + "\t1\t" + started + "\t" + ended + "\t" + rc + "\t-\tgo\t-\t-\t-\t-\t-\t" + usd + "\n"
	if err := os.WriteFile(filepath.Join(dir, "usage.tsv"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupProgress(t *testing.T, root string) string {
	t.Helper()
	fakeBins(t)
	specs := fakePATH(t)
	base := t.TempDir()
	queue := filepath.Join(base, "queue")
	for _, d := range []string{"pending", "launched"} {
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = root
	_ = specs
	return queue
}

func runProgress(t *testing.T, queue, roots, day string, now time.Time) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Progress(ProgressInput{
		Queue: queue, Roots: roots, Day: day,
		Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
	})
	return out.String(), errs.String(), code
}

// progress-counts-only-the-day: usage.tsv rows from two days under --roots yield
// cards= equal to the --day rows alone; rc0 counts the rows with rc=0.
func TestProgressCountsOnlyTheDay(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	queue := setupProgress(t, root)
	now, _ := time.Parse(time.RFC3339, "2026-09-15T12:00:00Z")
	writeProgressUsage(t, root, "slot-1", "j1", "2026-09-14T10:00:00Z", "2026-09-14T10:10:00Z", "0", "0.1000")
	writeProgressUsage(t, root, "slot-1", "j2", "2026-09-15T10:00:00Z", "2026-09-15T10:10:00Z", "0", "0.2000")
	writeProgressUsage(t, root, "slot-2", "j3", "2026-09-15T11:00:00Z", "2026-09-15T11:20:00Z", "1", "0.3000")
	out, errs, code := runProgress(t, queue, root, "2026-09-15", now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errs)
	}
	if !strings.Contains(out, "PROGRESS cards=2 rc0=1 ") {
		t.Fatalf("PROGRESS counts only the day, got:\n%s", out)
	}
}

// progress-parallelism-is-busy-over-span: four cards of 600 s each inside one
// 1200 s span print effective_parallelism=2.0; the slot count is nowhere in
// the arithmetic.
func TestProgressParallelismIsBusyOverSpan(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	queue := setupProgress(t, root)
	now, _ := time.Parse(time.RFC3339, "2026-09-15T12:00:00Z")
	writeProgressUsage(t, root, "slot-1", "a", "2026-09-15T09:00:00Z", "2026-09-15T09:10:00Z", "0", "0.1000")
	writeProgressUsage(t, root, "slot-2", "b", "2026-09-15T09:00:00Z", "2026-09-15T09:10:00Z", "0", "0.1000")
	writeProgressUsage(t, root, "slot-3", "c", "2026-09-15T09:10:00Z", "2026-09-15T09:20:00Z", "0", "0.1000")
	writeProgressUsage(t, root, "slot-4", "d", "2026-09-15T09:10:00Z", "2026-09-15T09:20:00Z", "0", "0.1000")
	out, errs, code := runProgress(t, queue, root, "2026-09-15", now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errs)
	}
	if !strings.Contains(out, "effective_parallelism=2.0") {
		t.Fatalf("parallelism is busy card-seconds over span seconds, got:\n%s", out)
	}
}

// estimate-uses-p90-and-parallelism: hours equals remaining x p90 / parallelism
// x 1.5, and remaining_cards counts pending, launched, unread PRs and twice the
// in-scope open issues -- an out-of-scope issue moves nothing.
func TestEstimateUsesP90AndParallelism(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	queue := setupProgress(t, root)
	now, _ := time.Parse(time.RFC3339, "2026-09-15T12:00:00Z")
	for _, name := range []string{"card-a.md", "card-b.md"} {
		if err := os.WriteFile(filepath.Join(queue, "pending", name), []byte("RESULT CARD-a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(queue, "launched", "card-c.md"), []byte("RESULT CARD-c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(queue, "sources.tsv")
	if err := os.WriteFile(src, []byte("issues\tmas-bandwidth/nova-tools#901\tfix\tpit-stop: the slot lock releases twice\tfix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queue, "POLICY"), []byte("scope-regex=pit-\nsources="+src+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specs := os.Getenv("NOVA_PULSE_FAKE_DIR")
	fakePrIssueGh(t, specs,
		`[{"number":7}]`,
		`[{"number":1,"title":"pit-stop one"},{"number":2,"title":"pit-stop two"},{"number":3,"title":"build a whole new dashboard"}]`)
	// Three cards of 600 s each inside a 900 s span: p90=600, parallelism=2.0.
	writeProgressUsage(t, root, "slot-1", "a", "2026-09-15T09:00:00Z", "2026-09-15T09:10:00Z", "0", "0.1000")
	writeProgressUsage(t, root, "slot-2", "b", "2026-09-15T09:00:00Z", "2026-09-15T09:10:00Z", "0", "0.1000")
	writeProgressUsage(t, root, "slot-3", "c", "2026-09-15T09:05:00Z", "2026-09-15T09:15:00Z", "0", "0.1000")
	out, errs, code := runProgress(t, queue, root, "2026-09-15", now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errs)
	}
	// remaining = pending(2) + launched(1) + unread PRs(1) + 2 x in-scope issues(2) = 8;
	// hours = 8 x 600 / 2.0 x 1.5 / 3600 = 1.0.
	if !strings.Contains(out, "ESTIMATE remaining_cards=8 hours=1.0") {
		t.Fatalf("ESTIMATE uses p90 and parallelism, got:\n%s", out)
	}
}

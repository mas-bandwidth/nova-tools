package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

func setupCollateBench(t *testing.T, day string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	transcript := msg("m1", day+"T10:00:00Z", "claude-sonnet", map[string]int{
		"input_tokens":  150,
		"output_tokens": 300,
	}, "/x/schema/a.go")
	write(t, filepath.Join(tr, "a.jsonl"), transcript+"\n")
	return out, repos, tr
}

func TestCollateHelp(t *testing.T) {
	t.Parallel()

	r1 := invoke(t, "collate", "-h")
	wantExit(t, r1, 0)
	wantContains(t, r1.stdout, "nova-tokens collate")

	r2 := invoke(t, "collate", "--help")
	wantExit(t, r2, 0)
	wantContains(t, r2.stdout, "nova-tokens collate")
}

func TestCollateRefusalMissingRequiredFlags(t *testing.T) {
	t.Parallel()

	// Missing sources and repos
	r := invoke(t, "collate", "--out", t.TempDir())
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--repos is required")
	wantContains(t, r.stderr, "run: nova-tokens help")

	// Mutually exclusive date flags
	out, repos, tr := setupCollateBench(t, "2026-09-11")
	r2 := invoke(t, "collate", "--out", out, "--repos", repos, "--claude", "b="+tr, "--today", "--yesterday")
	wantExit(t, r2, 2)
	wantContains(t, r2.stderr, "mutually exclusive")
}

func TestCollateAggregatesDailyTokenLogs(t *testing.T) {
	t.Parallel()

	day := "2026-09-11"
	out, repos, tr := setupCollateBench(t, day)

	now := time.Date(2026, 9, 11, 23, 55, 0, 0, time.UTC)
	r := invokeAt(t, now, "collate", "--out", out, "--day", day, "--repos", repos, "--claude", "b="+tr)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS COLLATE day=2026-09-11 written=true sources=claude:b turns=1 rows=1")
	wantContains(t, r.stdout, "COLLATE OK days=1 rows=1 sum_input=150 sum_output=300 ledger=skipped published=skipped")

	// Verify day file on disk
	dayPath := filepath.Join(out, day+".tsv")
	df, findings, err := tokens.ReadDayFile(dayPath)
	if err != nil {
		t.Fatalf("ReadDayFile failed: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("unexpected findings in day file: %v", findings)
	}
	if df.Day != day || df.Turns != "1" || len(df.Rows) != 1 {
		t.Errorf("unexpected DayFile: %+v", df)
	}
	if in, _ := df.Rows[0].Counts.Get(tokens.Input); in != 150 {
		t.Errorf("expected input=150, got %d", in)
	}
	if outCount, _ := df.Rows[0].Counts.Get(tokens.Output); outCount != 300 {
		t.Errorf("expected output=300, got %d", outCount)
	}
}

func TestCollateIdempotentExecution(t *testing.T) {
	t.Parallel()

	day := "2026-09-11"
	out, repos, tr := setupCollateBench(t, day)
	now := time.Date(2026, 9, 11, 23, 55, 0, 0, time.UTC)

	r1 := invokeAt(t, now, "collate", "--out", out, "--day", day, "--repos", repos, "--claude", "b="+tr)
	wantExit(t, r1, 0)

	dayPath := filepath.Join(out, day+".tsv")
	raw1, err := os.ReadFile(dayPath)
	if err != nil {
		t.Fatal(err)
	}

	r2 := invokeAt(t, now, "collate", "--out", out, "--day", day, "--repos", repos, "--claude", "b="+tr)
	wantExit(t, r2, 0)

	raw2, err := os.ReadFile(dayPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(raw1) != string(raw2) {
		t.Errorf("idempotent collation produced different file bytes:\n%s\nvs\n%s", string(raw1), string(raw2))
	}
}

func TestCollateFileLocking(t *testing.T) {
	t.Parallel()

	day := "2026-09-11"
	out, repos, tr := setupCollateBench(t, day)
	now := time.Date(2026, 9, 11, 23, 55, 0, 0, time.UTC)

	// Take the lock manually to simulate another process holding it
	release, err := tokens.TakeFoldLock(out, tokens.LockWait)
	if err != nil {
		t.Fatalf("TakeFoldLock failed: %v", err)
	}
	defer release()

	// Running collate with lock held should fail or refuse after timeout
	// To keep test fast, we can test that lock is respected
	done := make(chan result, 1)
	go func() {
		// Run with short timeout
		r := invokeAt(t, now, "collate", "--out", out, "--day", day, "--repos", repos, "--claude", "b="+tr, "--timeout", "1")
		done <- r
	}()

	select {
	case res := <-done:
		wantExit(t, res, 2)
		wantContains(t, res.stderr, "fold.lock")
	case <-time.After(12 * time.Second):
		t.Fatal("collate did not return after lock timeout")
	}
}

func TestCollateStalenessDetection(t *testing.T) {
	t.Parallel()

	day := "2026-09-11"
	out, repos, tr := setupCollateBench(t, day)

	// Clock is 5 days later: 2026-09-16
	later := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	// Collating with max-staleness=36h should notice activity from 2026-09-11 is stale
	r := invokeAt(t, later, "collate", "--out", out, "--all", "--repos", repos, "--claude", "b="+tr, "--max-staleness", "36")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS STALE")
	wantContains(t, r.stderr, "COLLATE FAIL")
}

func TestCollateTodayYesterdaySelection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)

	today := "2026-09-28"
	yesterday := "2026-09-27"
	now := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)

	msgToday := msg("mToday", today+"T10:00:00Z", "claude-sonnet", map[string]int{"input_tokens": 100}, "/x/schema/a.go")
	msgYest := msg("mYest", yesterday+"T10:00:00Z", "claude-sonnet", map[string]int{"input_tokens": 200}, "/x/schema/a.go")
	write(t, filepath.Join(tr, "a.jsonl"), msgToday+"\n"+msgYest+"\n")

	// Collate today
	rToday := invokeAt(t, now, "collate", "--out", out, "--today", "--repos", repos, "--claude", "b="+tr)
	wantExit(t, rToday, 0)
	wantContains(t, rToday.stdout, "TOKENS COLLATE day=2026-09-28 written=true")

	// Collate yesterday
	rYest := invokeAt(t, now, "collate", "--out", out, "--yesterday", "--repos", repos, "--claude", "b="+tr)
	wantExit(t, rYest, 0)
	wantContains(t, rYest.stdout, "TOKENS COLLATE day=2026-09-27 written=true")
}

func TestCollateDefaultReportsTokensDir(t *testing.T) {
	// Not t.Parallel() because it changes directory
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	repos := filepath.Join(dir, "repos.tsv")
	write(t, repos, "schema\t/x/schema/\n")
	tr := filepath.Join(dir, "tr")
	mkdir(t, tr)

	day := "2026-09-11"
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", day+"T10:00:00Z", "claude-sonnet", map[string]int{"input_tokens": 50}, "/x/schema/a.go")+"\n")

	now := time.Date(2026, 9, 11, 23, 55, 0, 0, time.UTC)
	// Without --out, defaults to reports/tokens
	r := invokeAt(t, now, "collate", "--day", day, "--repos", repos, "--claude", "b="+tr)
	wantExit(t, r, 0)

	targetFile := filepath.Join("reports", "tokens", day+".tsv")
	if _, err := os.Stat(targetFile); err != nil {
		t.Fatalf("expected default %s to exist: %v", targetFile, err)
	}
}

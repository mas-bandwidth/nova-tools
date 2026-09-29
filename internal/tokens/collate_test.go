package tokens

import (
	"os"
	"testing"
	"time"
)

func TestCheckStaleness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	// 1. No activity: should never be stale even if lastDay is very old or empty
	stale, exp, th := CheckStaleness("2026-09-20", now, 36*time.Hour, false)
	if stale {
		t.Errorf("expected not stale when hasActivity=false, got stale=true")
	}
	if exp != "2026-09-28" || th != 36 {
		t.Errorf("expected exp=2026-09-28 th=36, got exp=%s th=%d", exp, th)
	}

	stale, _, _ = CheckStaleness("", now, 36*time.Hour, false)
	if stale {
		t.Errorf("expected not stale when hasActivity=false and lastDay is empty")
	}

	// 2. Activity with empty lastDay -> stale
	stale, exp, th = CheckStaleness("", now, 36*time.Hour, true)
	if !stale {
		t.Errorf("expected stale when hasActivity=true and lastDay is empty")
	}
	if exp != "2026-09-28" || th != 36 {
		t.Errorf("expected exp=2026-09-28 th=36, got exp=%s th=%d", exp, th)
	}

	// 3. Activity with today's lastDay -> not stale
	stale, _, _ = CheckStaleness("2026-09-28", now, 36*time.Hour, true)
	if stale {
		t.Errorf("expected today's day file not to be stale")
	}

	// 4. Activity with yesterday's lastDay (2026-09-27)
	// Yesterday ended at 2026-09-28 00:00:00 UTC. At 12:00:00 UTC, elapsed time is 12h <= 36h -> not stale
	stale, _, _ = CheckStaleness("2026-09-27", now, 36*time.Hour, true)
	if stale {
		t.Errorf("expected yesterday's day file not to be stale within 36h")
	}

	// 5. Activity with day older than 36h
	// 2026-09-26 ended at 2026-09-27 00:00:00 UTC. At 2026-09-28 12:00:00 UTC, elapsed time is 36h -> not stale if <= 36h
	// At 2026-09-28 12:00:01 UTC, elapsed time is > 36h -> stale
	nowLater := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	stale, exp, th = CheckStaleness("2026-09-26", nowLater, 36*time.Hour, true)
	if !stale {
		t.Errorf("expected 2026-09-26 to be stale at 2026-09-28 13:00:00 (> 36h)")
	}
	if exp != "2026-09-28" || th != 36 {
		t.Errorf("expected exp=2026-09-28 th=36, got exp=%s th=%d", exp, th)
	}

	// 6. 2026-09-24 is 4 days old -> definitely stale
	stale, exp, th = CheckStaleness("2026-09-24", now, 36*time.Hour, true)
	if !stale {
		t.Errorf("expected 2026-09-24 to be stale at 2026-09-28")
	}

	// 7. Custom threshold (e.g. 10 hours)
	stale, exp, th = CheckStaleness("2026-09-27", now, 10*time.Hour, true)
	if !stale {
		t.Errorf("expected yesterday to be stale when threshold=10h and elapsed=12h")
	}
	if th != 10 {
		t.Errorf("expected threshold=10, got %d", th)
	}

	// 8. Disabled threshold (<= 0)
	stale, _, _ = CheckStaleness("2026-09-20", now, 0, true)
	if stale {
		t.Errorf("expected threshold=0 to disable staleness check")
	}
}

func makeRow(day, model, repo string, in, out int64, label string) *Row {
	r := &Row{
		Key: Key{
			Day:   day,
			Model: model,
			Repo:  repo,
			Unit:  "-",
		},
		sources: map[string]bool{label: true},
		bases:   map[string]bool{"utc": true},
	}
	r.Counts.Set(Input, in)
	r.Counts.Set(Output, out)
	return r
}

func TestCollateDay_NewDayFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := "2026-09-28"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	rows := []*Row{
		makeRow(day, "claude-sonnet", "nova-tools", 100, 200, "claude:runner"),
	}

	res, err := CollateDay(dir, day, rows, "5", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil {
		t.Fatalf("CollateDay failed: %v", err)
	}
	if !res.Written {
		t.Errorf("expected day file to be written")
	}
	if res.Rows != 1 {
		t.Errorf("expected 1 row, got %d", res.Rows)
	}
	if len(res.Sources) != 1 || res.Sources[0] != "claude:runner" {
		t.Errorf("expected sources [claude:runner], got %v", res.Sources)
	}

	// Verify file on disk
	filePath := Path(dir, day)
	df, findings, err := ReadDayFile(filePath)
	if err != nil {
		t.Fatalf("ReadDayFile failed: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("unexpected findings: %v", findings)
	}
	if df.Day != day || df.Turns != "5" || len(df.Rows) != 1 {
		t.Errorf("unexpected DayFile content: %+v", df)
	}
	if in, ok := df.Rows[0].Counts.Get(Input); !ok || in != 100 {
		t.Errorf("expected input=100, got %d (ok=%t)", in, ok)
	}
}

func TestCollateDay_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := "2026-09-28"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	rows := []*Row{
		makeRow(day, "claude-sonnet", "nova-tools", 100, 200, "claude:runner"),
	}

	// First collation
	res1, err := CollateDay(dir, day, rows, "5", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil || !res1.Written {
		t.Fatalf("first CollateDay failed: %v", err)
	}

	filePath := Path(dir, day)
	raw1, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}

	// Second collation with identical rows
	res2, err := CollateDay(dir, day, rows, "5", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil || !res2.Written {
		t.Fatalf("second CollateDay failed: %v", err)
	}

	raw2, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}

	if string(raw1) != string(raw2) {
		t.Errorf("expected byte-identical file after idempotent collation; diff:\n%s\nvs\n%s", string(raw1), string(raw2))
	}
}

func TestCollateDay_ShrinkRefusal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := "2026-09-28"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	rows1 := []*Row{
		makeRow(day, "claude-sonnet", "nova-tools", 1000, 200, "claude:runner"),
	}

	res1, err := CollateDay(dir, day, rows1, "5", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil || !res1.Written {
		t.Fatalf("initial CollateDay failed: %v", err)
	}

	// Smaller rows (shrunk input: 1000 -> 500)
	rows2 := []*Row{
		makeRow(day, "claude-sonnet", "nova-tools", 500, 200, "claude:runner"),
	}

	// Without allowShrink: should refuse
	res2, err := CollateDay(dir, day, rows2, "5", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Shrank {
		t.Errorf("expected res2.Shrank == true")
	}
	if res2.Written {
		t.Errorf("expected res2.Written == false without allowShrink")
	}

	// With allowShrink: should succeed
	res3, err := CollateDay(dir, day, rows2, "5", []string{"claude:runner"}, true, "v1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	if !res3.Shrank {
		t.Errorf("expected res3.Shrank == true")
	}
	if !res3.Written {
		t.Errorf("expected res3.Written == true with allowShrink")
	}
}

func TestCollateDay_SourceMerging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := "2026-09-28"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	// Step 1: Initial file with two rows from bus:emma and claude:runner
	rows1 := []*Row{
		makeRow(day, "gpt-4", "schema", 300, 100, "bus:emma"),
		makeRow(day, "claude-sonnet", "nova-tools", 1000, 200, "claude:runner"),
	}
	res1, err := CollateDay(dir, day, rows1, "10", []string{"bus:emma", "claude:runner"}, false, "v1.0.0", now)
	if err != nil || !res1.Written {
		t.Fatalf("initial CollateDay failed: %v", err)
	}

	// Step 2: Collation declaring only claude:runner with updated tokens (1200)
	rows2 := []*Row{
		makeRow(day, "claude-sonnet", "nova-tools", 1200, 200, "claude:runner"),
	}
	res2, err := CollateDay(dir, day, rows2, "12", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil || !res2.Written {
		t.Fatalf("merged CollateDay failed: %v", err)
	}

	// Verify that both rows are present: bus:emma was retained, claude:runner was updated
	df, findings, err := ReadDayFile(Path(dir, day))
	if err != nil || len(findings) > 0 {
		t.Fatalf("ReadDayFile failed: %v, findings: %v", err, findings)
	}
	if len(df.Rows) != 2 {
		t.Fatalf("expected 2 merged rows, got %d", len(df.Rows))
	}
	// Turns should be Dash because rows were retained from another source
	if df.Turns != Dash {
		t.Errorf("expected Turns=%s when rows retained, got %s", Dash, df.Turns)
	}
}

func TestCollateDay_PartialCollision(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := "2026-09-28"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	// Create a row that was blended across both sources
	r := &Row{
		Key: Key{
			Day:   day,
			Model: "claude-sonnet",
			Repo:  "nova-tools",
			Unit:  "-",
		},
		sources: map[string]bool{"bus:emma": true, "claude:runner": true},
		bases:   map[string]bool{"utc": true},
	}
	r.Counts.Set(Input, 1000)

	res1, err := CollateDay(dir, day, []*Row{r}, "5", []string{"bus:emma", "claude:runner"}, false, "v1.0.0", now)
	if err != nil || !res1.Written {
		t.Fatalf("initial CollateDay failed: %v", err)
	}

	// New fold only declares claude:runner: cannot decompose the blended row -> TOKENS PARTIAL
	r2 := makeRow(day, "claude-sonnet", "nova-tools", 600, 100, "claude:runner")
	res2, err := CollateDay(dir, day, []*Row{r2}, "5", []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Partial {
		t.Errorf("expected res2.Partial == true on blended row collision")
	}
	if res2.Written {
		t.Errorf("expected res2.Written == false on partial collision")
	}
}

func TestCollateDay_EmptyDayDoesNotWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := "2026-09-28"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	res, err := CollateDay(dir, day, nil, Dash, []string{"claude:runner"}, false, "v1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written {
		t.Errorf("expected res.Written == false for empty day")
	}
	if _, err := os.Stat(Path(dir, day)); !os.IsNotExist(err) {
		t.Errorf("expected no day file created for empty day, but file exists")
	}
}

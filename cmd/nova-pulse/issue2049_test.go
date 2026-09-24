package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// TestIssue2049: "status and log: the coordinator's questions answered by a verb".
// nova-tools#2049 asks for `nova-pulse log --since` as a verb that reads
// primary data (pulse.log), the way bin/nova-log.sh did before. On base the
// verb does not exist; after the fix it reads the queue's pulse.log and
// filters by --since duration.
func TestIssue2049(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	logBody := "11:50:00Z GATE GREEN repo=mas-bandwidth/nova-tools\n" +
		"11:59:10Z HARVEST repo=mas-bandwidth/nova-tools\n" +
		"12:00:05Z SWEEP repo=mas-bandwidth/nova-tools enqueued=1\n"
	writePulseLog(t, queue, logBody, time.Date(2026, 9, 16, 12, 0, 5, 0, time.UTC))

	var out, errb bytes.Buffer
	code := run([]string{"log", "--queue", queue, "--since", "5m"}, &out, &errb, now)
	if code != 0 {
		t.Fatalf("log exit = %d, want 0; stderr=%s", code, errb.String())
	}
	// The line at 11:50:00Z is 10 minutes before the reference time
	// (12:00:00Z) and must not appear; the lines at 11:59:10Z (50s before)
	// and 12:00:05Z (5s after) are within the 5m window.
	got := out.String()
	if strings.Contains(got, "11:50:00Z") {
		t.Errorf("log printed the line older than --since=5m:\n%s", got)
	}
	if !strings.Contains(got, "11:59:10Z") || !strings.Contains(got, "12:00:05Z") {
		t.Errorf("log did not print the lines within --since=5m:\n%s", got)
	}
	// Without --since, all lines appear.
	var out2, errb2 bytes.Buffer
	code2 := run([]string{"log", "--queue", queue}, &out2, &errb2, now)
	if code2 != 0 {
		t.Fatalf("log (no --since) exit = %d; stderr=%s", code2, errb2.String())
	}
	for _, want := range []string{"11:50:00Z", "11:59:10Z", "12:00:05Z"} {
		if !strings.Contains(out2.String(), want) {
			t.Errorf("log (no --since) did not print %q:\n%s", want, out2.String())
		}
	}

	// Without --queue the verb refuses.
	var out3, errb3 bytes.Buffer
	code3 := run([]string{"log"}, &out3, &errb3, now)
	if code3 != 2 {
		t.Fatalf("log without --queue exit = %d, want 2", code3)
	}
	if !strings.Contains(errb3.String(), "--queue is required") {
		t.Errorf("log without --queue did not name the remedy:\n%s", errb3.String())
	}
}

// writePulseLog writes pulse.log with mtime set to the last append, which is
// what dates the time-only records.
func writePulseLog(t *testing.T, queue, body string, last time.Time) {
	t.Helper()
	path := filepath.Join(queue, "pulse.log")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, last, last); err != nil {
		t.Fatal(err)
	}
}

func logSince(t *testing.T, queue, since string, now time.Time) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"log", "--queue", queue, "--since", since}, &out, &errb, now); code != 0 {
		t.Fatalf("log exit = %d, want 0; stderr=%s", code, errb.String())
	}
	return out.String()
}

// TestIssue2049MultiDay: stella's hold on #2832 at ba153c1f. pulse.log rows
// are HH:MM:SSZ only; a persistent queue spans days, so yesterday's 11:59:10Z
// must not be read as today's and survive --since 5m at noon.
func TestIssue2049MultiDay(t *testing.T) {
	queue := t.TempDir()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	body := "11:59:10Z OLD-YESTERDAY\n" +
		"18:00:00Z EVENING-YESTERDAY\n" +
		"03:00:00Z NIGHT-TODAY\n" +
		"11:58:00Z RECENT-TODAY\n"
	writePulseLog(t, queue, body, time.Date(2026, 9, 16, 11, 58, 0, 0, time.UTC))
	got := logSince(t, queue, "5m", now)
	for _, stale := range []string{"OLD-YESTERDAY", "EVENING-YESTERDAY", "NIGHT-TODAY"} {
		if strings.Contains(got, stale) {
			t.Errorf("--since 5m printed stale %s:\n%s", stale, got)
		}
	}
	if !strings.Contains(got, "RECENT-TODAY") {
		t.Errorf("--since 5m dropped the recent record:\n%s", got)
	}
	// A 25h window reaches back over the midnight to yesterday's rows.
	got = logSince(t, queue, "25h", now)
	for _, want := range []string{"OLD-YESTERDAY", "EVENING-YESTERDAY", "NIGHT-TODAY", "RECENT-TODAY"} {
		if !strings.Contains(got, want) {
			t.Errorf("--since 25h dropped %s:\n%s", want, got)
		}
	}
}

// TestIssue2049Midnight: just after midnight, yesterday's late rows are not
// future rows of today; only the rows inside the window print.
func TestIssue2049Midnight(t *testing.T) {
	queue := t.TempDir()
	now := time.Date(2026, 9, 17, 0, 5, 0, 0, time.UTC)
	body := "12:00:00Z NOON-YESTERDAY\n" +
		"23:58:00Z LATE-YESTERDAY\n" +
		"00:02:00Z AFTER-MIDNIGHT\n"
	writePulseLog(t, queue, body, time.Date(2026, 9, 17, 0, 2, 0, 0, time.UTC))
	got := logSince(t, queue, "10m", now)
	if strings.Contains(got, "NOON-YESTERDAY") {
		t.Errorf("--since 10m at 00:05Z printed yesterday noon:\n%s", got)
	}
	if !strings.Contains(got, "LATE-YESTERDAY") || !strings.Contains(got, "AFTER-MIDNIGHT") {
		t.Errorf("--since 10m at 00:05Z dropped a row inside the window:\n%s", got)
	}
	// The last row itself before midnight: mtime 23:58Z yesterday, now 00:05Z.
	writePulseLog(t, queue, "12:00:00Z NOON\n23:58:00Z LATE\n", time.Date(2026, 9, 16, 23, 58, 0, 0, time.UTC))
	got = logSince(t, queue, "10m", now)
	if strings.Contains(got, "NOON") || !strings.Contains(got, "LATE") {
		t.Errorf("--since 10m, last append before midnight: got\n%s", got)
	}
}

// TestIssue2049Dated: a row that carries its own date is read as written.
func TestIssue2049Dated(t *testing.T) {
	queue := t.TempDir()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	body := "2026-09-15T11:59:00Z DATED-OLD\n2026-09-16T11:59:00Z DATED-NEW\n"
	writePulseLog(t, queue, body, time.Date(2026, 9, 16, 11, 59, 0, 0, time.UTC))
	got := logSince(t, queue, "5m", now)
	if strings.Contains(got, "DATED-OLD") || !strings.Contains(got, "DATED-NEW") {
		t.Errorf("dated rows: got\n%s", got)
	}
}

// TestIssue2049QuietQueueGap: Stella's hold on #2832 (HOLD 7).
// When a quiet queue leaves a >24h gap between entries (e.g. 11:59:00Z two days ago
// followed by 12:00:00Z today), time-only records caused logTimes to date the older
// entry as today because 11:59 <= 12:00 did not indicate a midnight wrap.
// Writing full UTC timestamps at the source ensures the stale row is properly excluded
// by --since 5m. Fails at bc765217.
func TestIssue2049QuietQueueGap(t *testing.T) {
	queue := t.TempDir()
	now := time.Date(2026, 9, 14, 11, 59, 0, 0, time.UTC)
	w := pulse.NewWiring(pulse.WiringInput{
		Queue: queue,
		Now:   func() time.Time { return now },
	})
	w.Log("OLD-ROW repo=mas-bandwidth/nova-tools\n")

	// 2 days later on a quiet queue: fresh row at noon
	now = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	w.Log("FRESH-ROW repo=mas-bandwidth/nova-tools\n")

	got := logSince(t, queue, "5m", now)
	if strings.Contains(got, "OLD-ROW") {
		t.Errorf("--since 5m included stale row written two days earlier:\n%s", got)
	}
	if !strings.Contains(got, "FRESH-ROW") {
		t.Errorf("--since 5m dropped fresh row:\n%s", got)
	}

	gotAll := logSince(t, queue, "50h", now)
	if !strings.Contains(gotAll, "OLD-ROW") || !strings.Contains(gotAll, "FRESH-ROW") {
		t.Errorf("--since 50h did not return both rows:\n%s", gotAll)
	}
}


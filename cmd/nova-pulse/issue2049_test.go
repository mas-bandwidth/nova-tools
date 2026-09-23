package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if err := os.WriteFile(filepath.Join(queue, "pulse.log"), []byte(logBody), 0o644); err != nil {
		t.Fatal(err)
	}

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

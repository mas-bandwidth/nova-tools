package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIssue2049WriterUTC tests that pulse.log writer writes full UTC timestamps
// (2006-01-02T15:04:05Z) so records across days carry their own date.
func TestIssue2049WriterUTC(t *testing.T) {
	queue := t.TempDir()
	now := time.Date(2026, 9, 14, 11, 59, 0, 0, time.UTC)
	w := NewWiring(WiringInput{
		Queue: queue,
		Now:   func() time.Time { return now },
	})
	w.Log("GATE GREEN repo=mas-bandwidth/nova-tools\n")

	raw, err := os.ReadFile(filepath.Join(queue, "pulse.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	want := "2026-09-14T11:59:00Z GATE GREEN repo=mas-bandwidth/nova-tools\n"
	if got != want {
		t.Fatalf("pulse.log = %q, want %q", got, want)
	}

	// Two days later on a quiet queue:
	now = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	w.Log("GATE GREEN repo=mas-bandwidth/nova-tools\n")

	raw2, err := os.ReadFile(filepath.Join(queue, "pulse.log"))
	if err != nil {
		t.Fatal(err)
	}
	got2 := string(raw2)
	want2 := "2026-09-14T11:59:00Z GATE GREEN repo=mas-bandwidth/nova-tools\n" +
		"2026-09-16T12:00:00Z GATE GREEN repo=mas-bandwidth/nova-tools\n"
	if got2 != want2 {
		t.Fatalf("pulse.log = %q, want %q", got2, want2)
	}
}

// TestIssue2049WriterToBuffer tests writing to WiringInput.Log when non-nil.
func TestIssue2049WriterToBuffer(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC)
	w := NewWiring(WiringInput{
		Log: &buf,
		Now: func() time.Time { return now },
	})
	w.Log("SWEEP repo=mas-bandwidth/nova-tools\n")

	got := buf.String()
	want := "2026-09-15T08:30:00Z SWEEP repo=mas-bandwidth/nova-tools\n"
	if got != want {
		t.Fatalf("Log buffer = %q, want %q", got, want)
	}
}

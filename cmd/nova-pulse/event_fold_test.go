package main

// event_fold_test.go covers the two verbs of nova-tools #2563 at the door: what they refuse
// and what they answer with no store at all. The stream and the fold themselves are tested
// in internal/events against a fake; here the question is only whether a person pasting the
// line gets a sentence that says what the input WANTS (docs/ONBOARDING.md point 2).

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pulseRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Date(2026, 9, 22, 14, 5, 0, 0, time.UTC))
	return code, out.String(), errb.String()
}

func TestEventRefusesWhatItCannotGuess(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no label", []string{"event", "--event", "ok", "--print"}, "--label is required"},
		{"no event", []string{"event", "--label", "card-1", "--print"}, "--event is required"},
		{"an unknown event", []string{"event", "--label", "card-1", "--event", "done", "--print"}, "queued|leased"},
		{"no store and no --print", []string{"event", "--label", "card-1", "--event", "ok"}, "--store is required"},
		{"a stamp that is not RFC3339", []string{"event", "--label", "card-1", "--event", "ok", "--at", "yesterday", "--print"}, "RFC3339"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errb := pulseRun(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (could not run); stderr=%s", code, errb)
			}
			if !strings.Contains(errb, tc.want) {
				t.Fatalf("stderr = %q, want it to name %q", errb, tc.want)
			}
		})
	}
}

// --print is the offline first run: the entry a writer would XADD, with nothing written.
func TestEventPrintShowsTheEntryAndWritesNothing(t *testing.T) {
	code, out, errb := pulseRun(t, "event", "--label", "card-42", "--event", "ok",
		"--bench", "studio", "--model", "fable", "--route", "studio", "--usd", "0.11", "--print")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb)
	}
	for _, want := range []string{"label\tcard-42", "event\tok", "usd\t0.11", "at\t2026-09-22T14:05:00Z", "EVENT ev:cards"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not carry %q:\n%s", want, out)
		}
	}
}

func TestFoldRefusesWhatItCannotGuess(t *testing.T) {
	db := filepath.Join(t.TempDir(), "ev.sqlite")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no db", []string{"fold", "--store", "store.invalid:6380"}, "--db is required"},
		{"no store", []string{"fold", "--db", db}, "--store is required"},
		{"two runs at once", []string{"fold", "--db", db, "--init", "--report"}, "name one"},
		{"a count of none", []string{"fold", "--db", db, "--count", "0", "--init"}, "--count wants"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errb := pulseRun(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (could not run); stderr=%s", code, errb)
			}
			if !strings.Contains(errb, tc.want) {
				t.Fatalf("stderr = %q, want it to name %q", errb, tc.want)
			}
		})
	}
}

// --init, --report and --dump are the three runs that read the file alone, which is what
// makes the help's example block runnable with no fleet store in the room.
func TestFoldInitReportAndDumpNeedNoStore(t *testing.T) {
	db := filepath.Join(t.TempDir(), "ev.sqlite")

	code, out, errb := pulseRun(t, "fold", "--db", db, "--init")
	if code != 0 {
		t.Fatalf("--init exit = %d, want 0; stderr=%s", code, errb)
	}
	if !strings.Contains(out, "tables=attempts,reads,landings") {
		t.Fatalf("--init printed %q, want the three tables named", out)
	}

	code, out, errb = pulseRun(t, "fold", "--db", db, "--report")
	if code != 0 {
		t.Fatalf("--report exit = %d, want 0; stderr=%s", code, errb)
	}
	for _, want := range []string{"# totals", "# by_model_route", "usd_per_landed"} {
		if !strings.Contains(out, want) {
			t.Errorf("--report does not carry %q:\n%s", want, out)
		}
	}

	code, out, errb = pulseRun(t, "fold", "--db", db, "--dump")
	if code != 0 {
		t.Fatalf("--dump exit = %d, want 0; stderr=%s", code, errb)
	}
	if !strings.HasPrefix(out, "table\tevent_id\tlabel") {
		t.Fatalf("--dump printed %q, want the TSV header first", out)
	}
}

// --rebuild replays into a FRESH file; pointed at one that exists it says so and names the
// remedy, rather than folding a second copy of the stream into someone's record.
func TestFoldRebuildRefusesAFileThatExists(t *testing.T) {
	db := filepath.Join(t.TempDir(), "ev.sqlite")
	if code, _, errb := pulseRun(t, "fold", "--db", db, "--init"); code != 0 {
		t.Fatalf("--init exit = %d; stderr=%s", code, errb)
	}
	code, _, errb := pulseRun(t, "fold", "--db", db, "--rebuild", "--store", "store.invalid:6380")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errb)
	}
	if !strings.Contains(errb, "already exists") || !strings.Contains(errb, "name a path that does not exist") {
		t.Fatalf("stderr = %q, want it to name the file and the remedy", errb)
	}
}

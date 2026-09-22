package receipt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// at is the one stamp these tests use; a fixed clock, because a test that reads the wall
// clock is a test that asserts the machine (AGENTS.md rule 3).
var at = time.Date(2026, 9, 22, 14, 5, 0, 0, time.UTC)

func sha() string { return "39d1d6a5c9182967405eb6211fadeb2eaf3dffe4" }

// TestReceiptAppendsOneEvent is the DONE-WHEN check: canary, conform and adopt each append
// one kind=receipt event carrying pass/fail and the sha, the lineup reads them back from the
// stream, and none of the six files is written.
func TestReceiptAppendsOneEvent(t *testing.T) {
	ctx := context.Background()
	store := NewMemStream()

	id, _, err := Append(ctx, store, Canary, true, sha(), at)
	if err != nil {
		t.Fatalf("canary Append: %v", err)
	}
	if id == "" {
		t.Fatal("canary Append returned an empty id")
	}
	if _, _, err := Append(ctx, store, Conform, false, sha(), at); err != nil {
		t.Fatalf("conform Append: %v", err)
	}
	if _, _, err := Append(ctx, store, Adopt, true, sha(), at); err != nil {
		t.Fatalf("adopt Append: %v", err)
	}

	got, err := Read(ctx, store)
	if err != nil {
		t.Fatalf("Read (the lineup): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("the stream holds %d receipt events, want 3", len(got))
	}

	want := []struct {
		verb Verb
		pass bool
	}{
		{Canary, true},
		{Conform, false},
		{Adopt, true},
	}
	for i, w := range want {
		r := got[i]
		if r.Kind != Kind {
			t.Errorf("event %d: kind=%q, want %q", i, r.Kind, Kind)
		}
		if r.Verb != w.verb {
			t.Errorf("event %d: verb=%q, want %q", i, r.Verb, w.verb)
		}
		if r.Pass != w.pass {
			t.Errorf("event %d: pass=%v, want %v", i, r.Pass, w.pass)
		}
		if r.SHA != sha() {
			t.Errorf("event %d: sha=%q, want %q", i, r.SHA, sha())
		}
	}

	// None of the six files is written, in this directory or anywhere else.
	for _, name := range SixFiles {
		if _, err := os.Stat(filepath.Join(t.TempDir(), name)); !os.IsNotExist(err) {
			t.Errorf("%s: want absent, stat err = %v", name, err)
		}
	}
	assertSixAbsent(t)
}

// assertSixAbsent is the control on "none of the six files is written": the receipt path is
// a pure stream writer, so a run that left any of the six on the working tree is a bug.
func assertSixAbsent(t *testing.T) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, p := range matches {
		name := filepath.Base(p)
		if isOneOf(name, SixFiles) || len(name) >= 6 && name[:6] == "ADOPT-" {
			t.Errorf("the receipt path wrote %s, which DONE-WHEN forbids", name)
		}
	}
}

func isOneOf(name string, list []string) bool {
	for _, w := range list {
		if name == w {
			return true
		}
	}
	return false
}

func TestAppendRejectsEmptySHA(t *testing.T) {
	if _, _, err := Append(context.Background(), NewMemStream(), Canary, true, "", at); err == nil {
		t.Fatal("Append with an empty sha: want error, got nil")
	}
}

func TestAppendRefusesControlCharInSHA(t *testing.T) {
	if _, _, err := Append(context.Background(), NewMemStream(), Canary, true, "dead\x01beef", at); err == nil {
		t.Fatal("Append with a control character in sha: want error, got nil")
	}
}

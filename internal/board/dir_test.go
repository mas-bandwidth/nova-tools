package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func card(id, text string) string {
	return Event{Verb: "card", ID: id, As: "rowan", At: mustTime2("2026-09-10T09:00:00Z"),
		Hash: HashOf(Tail(text)), Owner: "rowan", By: "2026-09-12T09:00:00Z",
		Default: "rowan files it", Tail: Tail(text)}.Render()
}

func mustTime2(s string) time.Time {
	when, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return when.UTC()
}

// CREATION IS EXCLUSIVE AGAINST HAND-MADE FILES, and the card file is never truncated. An
// id that already exists is a refusal, and no card file is replaced, ever.
func TestCreationIsExclusiveAndNeverTruncates(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Append(card(idA, "the first thing")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, idA+".board")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), Version+"\n") {
		t.Errorf("a card file does not begin with its version line:\n%s", raw)
	}
	if err := d.Append(card(idA, "a second thing under the same id")); err != ErrExists {
		t.Errorf("a second add under one id returned %v, want ErrExists", err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(raw) {
		t.Error("the existing card file's bytes changed")
	}
	// A later event for a card this backend does not hold is not a card file it creates.
	if err := d.Append(Event{Verb: "taken", ID: idB, Ev: "0000000000a1", After: idB,
		As: "bo", At: mustTime2("2026-09-10T09:05:00Z")}.Render()); err != ErrNoCard {
		t.Errorf("appending a take to no card returned %v, want ErrNoCard", err)
	}
	if _, err := os.Stat(filepath.Join(dir, idB+".board")); !os.IsNotExist(err) {
		t.Error("a take created a card file")
	}
}

// A file that is not `<thirty-two hex>.board` is COUNTED and never read as a card; blank
// lines and # comments are ignored, as everywhere else in this family's files; and a card
// file without its version line is an error, because a later format read as this one would
// be entries nobody wrote.
func TestTheDirectoryCountsWhatItWillNotRead(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Append(card(idA, "the first thing")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "notes.txt", "cafe.board", ".DS_Store"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, idA+".board"),
		[]byte(Version+"\n\n# a note somebody left in the file\n"+card(idA, "the first thing")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := d.Events()
	if err != nil {
		t.Fatal(err)
	}
	if log.UnparsedFiles != 4 {
		t.Errorf("unparsed files = %d, want 4", log.UnparsedFiles)
	}
	b := Derive(log, mustTime2("2026-09-10T09:30:00Z"), 10*time.Minute)
	if len(b.Cards) != 1 || b.Unparsed != 0 {
		t.Errorf("blank lines and # comments must be ignored rather than counted: cards=%d unparsed=%d", len(b.Cards), b.Unparsed)
	}

	// The version line is not optional.
	if err := os.WriteFile(filepath.Join(dir, idB+".board"), []byte(card(idB, "a file from a later format")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Events(); err == nil {
		t.Error("a card file with no version line was read as this format")
	} else if !strings.Contains(err.Error(), Version) {
		t.Errorf("the error does not name the version line: %v", err)
	}
}

// The directory backend APPENDS AND NEVER RUNS GIT, and it takes no lock: the fold is over
// the card files and there is no shared file and no index.
func TestTheDirectoryBackendWritesOneFilePerCardAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{idA, idB} {
		if err := d.Append(card(id, "a thing owed")); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("the backend wrote %d files, want one per card and no index, no lock, no git", len(entries))
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".board") {
			t.Errorf("the backend wrote %q", e.Name())
		}
	}
	// --dir wants a directory, and says so.
	if _, err := NewDir(filepath.Join(dir, idA+".board")); err == nil {
		t.Error("a file was accepted as a board directory")
	} else if !strings.Contains(err.Error(), "--dir wants") {
		t.Errorf("the refusal does not say what --dir wants: %v", err)
	}
	if _, err := NewDir(filepath.Join(dir, "nowhere")); err == nil {
		t.Error("a directory that does not exist was accepted; a tool that made one would be guessing")
	}
}

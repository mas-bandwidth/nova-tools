package cairn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The blast radius of one damaged session, as the spec states it. A record
// whose PATH holds no record refuses every verb addressed to it; a record whose
// CONTENT is damaged is flagged by index and refused by receipt, while open and
// append, which read it only to find their own entry, still work and rewrite
// nothing. Every other session is listed throughout.
func TestADamagedRecordIsFlaggedByIndexRefusedByReceiptAndStillAppendable(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	good := "# g\n\n## 2026-09-29T08:00:00Z — e1\n\nw\n"
	dup := good + "\n## 2026-09-29T08:00:00Z — e1\n\nw2\n"
	badStamp := "## 2026-99-99T08:00:00Z — e1\n\nx\n"
	for id, body := range map[string]string{"good": good, "dup": dup, "badstamp": badStamp} {
		if err := os.WriteFile(benchFile(store, id), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(store, "nowhere.md"), benchFile(store, "gone")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	res, err := IndexAll(store, "", 0)
	if err != nil || res.Sessions != 4 || res.Total != 1 || len(res.Flagged) != 3 {
		t.Fatalf("the store: %+v %v", res, err)
	}
	for _, f := range res.Flagged {
		if strings.Contains(f.Cause, "cannot index the store") {
			t.Errorf("a flagged cause is the session's own, not a store-level refusal: %q", f.Cause)
		}
	}
	for _, id := range []string{"dup", "badstamp"} {
		if r, err := IndexAll(store, id, 0); err != nil || len(r.Flagged) != 1 || r.Flagged[0].Session != id {
			t.Errorf("index --session %s: want its one flagged row, got %+v %v", id, r, err)
		}
		if _, err := Receipt(store, id, "e1"); err == nil {
			t.Errorf("receipt %s: want a refusal", id)
		}
		before, _ := os.ReadFile(benchFile(store, id))
		if err := Open(store, id, "", benchNow, PublishManual); err != nil {
			t.Errorf("open %s: %v", id, err)
		}
		if _, err := Append(store, id, "new", "words", "", benchNow, PublishManual); err != nil {
			t.Errorf("append %s: %v", id, err)
		}
		after, _ := os.ReadFile(benchFile(store, id))
		if !strings.HasPrefix(string(after), string(before)) || !strings.Contains(string(after[len(before):]), "— new") {
			t.Errorf("append %s rewrote the record or did not add its section: %q", id, after)
		}
	}
	if r, err := Receipt(store, "good", "e1"); err != nil || r.ID != "e1" {
		t.Fatalf("a good session beside the damaged ones: %+v %v", r, err)
	}
}

// A directory named <id>.md and a file named <id>.MD are not session files:
// index neither lists nor flags them, and the coverage ledger skips them. A
// verb addressed to the directory refuses (TestNonRegularRecordPathIsRefused-
// ByEveryVerb).
func TestADirectoryAndACaseTwinAreNotSessionsAndAreNotFlagged(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	good := "# g\n\n## 2026-09-29T08:00:00Z — e1\n\nw\n"
	if err := os.WriteFile(benchFile(store, "s1"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(benchFile(store, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !foldsCase(t) {
		if err := os.WriteFile(filepath.Join(store, "s1.MD"), []byte(good), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := IndexAll(store, "", 0)
	if err != nil || res.Sessions != 1 || res.Total != 1 || len(res.Flagged) != 0 {
		t.Fatalf("index over a directory and a case twin: %+v %v", res, err)
	}
	if led := Coverage(store); led.Sessions != 1 || led.Entries != 1 {
		t.Fatalf("coverage: %+v", led)
	}
}

// In the tool's own shape index reads the entry files, and a damaged one is an
// error for the whole call, as the spec says; it is not a flagged row.
func TestAnOwnShapeDamagedEntryRefusesTheIndex(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	if err := Open(store, "s1", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(store, "s1", "e1", "words", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath(store, "s1", "e1"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := IndexAll(store, "", 0); err == nil || len(res.Flagged) != 0 {
		t.Fatalf("want a refusal of the whole index, got %+v %v", res, err)
	}
}

// The whitespace-only rule differs by shape and the docs say so: the own shape
// stores the words exactly, so words of whitespace only are stored and only no
// words at all is refused.
func TestWhitespaceOnlyWordsAreStoredInTheOwnShapeAndNoWordsAreRefused(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	if err := Open(store, "s1", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(store, "s1", "e1", "  \n", "", benchNow, PublishManual); err != nil {
		t.Fatalf("whitespace-only words: %v", err)
	}
	if got, err := EntryText(store, "s1", "e1"); err != nil || got != "  \n" {
		t.Fatalf("the words were not stored exactly: %q %v", got, err)
	}
	if _, err := Append(store, "s1", "e2", "", "", benchNow, PublishManual); err == nil || !strings.Contains(err.Error(), "empty note stores nothing") {
		t.Fatalf("no words at all: %v", err)
	}
}

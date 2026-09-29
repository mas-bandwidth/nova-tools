package cairn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The blast radius of one damaged session, as the spec states it. A record
// whose PATH holds no record refuses every verb addressed to it; a record whose
// CONTENT is damaged is flagged by index and refused by receipt and by append,
// which names the damage and a command that lists the headings; open is a no-op.
// Every other session is listed throughout.
func TestADamagedRecordIsFlaggedByIndexAndRefusedByReceiptAndAppend(t *testing.T) {
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
	for id, damage := range map[string]string{"dup": `duplicate entry "e1"`, "badstamp": "invalid entry heading"} {
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
		_, err := Append(store, id, "new", "words", "", benchNow, PublishManual)
		var rp *RecordPathError
		if !errors.As(err, &rp) {
			t.Fatalf("append %s: want a refusal, got %v", id, err)
		}
		for _, w := range []string{"cannot append an entry", benchFile(store, id), "is damaged", damage, "grep -n '^## ' -- ", "run the same command again"} {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("append %s: %q lacks %q", id, err, w)
			}
		}
		if after, _ := os.ReadFile(benchFile(store, id)); string(after) != string(before) {
			t.Errorf("a refused append changed the record %s", id)
		}
	}
	if r, err := Receipt(store, "good", "e1"); err != nil || r.ID != "e1" {
		t.Fatalf("a good session beside the damaged ones: %+v %v", r, err)
	}
	if _, err := Append(store, "good", "e2", "words", "", benchNow, PublishManual); err != nil {
		t.Fatalf("append to the good session: %v", err)
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

// In the own shape a session file that is a directory or a link, and an entry
// directory that is a link, are each that session's defect: index flags the
// session, lists none of its entries, and lists the others.
func TestAnOwnShapeSessionThatIsADirectoryOrALinkIsFlagged(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	for _, id := range []string{"good", "adir", "gone", "linked"} {
		layOwn(t, store, id)
	}
	replace := func(path string, with func(string) error) {
		if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
			t.Fatal(err)
		}
		if err := with(path); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}
	replace(sessionFile(store, "adir"), func(p string) error { return os.Mkdir(p, 0o755) })
	replace(sessionFile(store, "gone"), func(p string) error { return os.Symlink(filepath.Join(store, "nowhere"), p) })
	replace(filepath.Join(store, "entries", "linked"), func(p string) error { return os.Symlink(t.TempDir(), p) })
	res, err := IndexAll(store, "", 0)
	if err != nil || res.Sessions != 4 || len(res.Flagged) != 3 || len(res.Rows) != 1 || res.Rows[0].Session != "good" {
		t.Fatalf("index: %+v %v", res, err)
	}
	for _, f := range res.Flagged {
		if want := map[string]string{"adir": "is a directory", "gone": "is a symbolic link", "linked": "is a symbolic link"}[f.Session]; !strings.Contains(f.Cause, want) {
			t.Errorf("session %s: cause %q lacks %q", f.Session, f.Cause, want)
		}
	}
}

// An own-shape append that the record refuses stores nothing: no entry file,
// no pointer line, no log line. The record is opened and read before the entry
// is written.
func TestARefusedOwnAppendStoresNothing(t *testing.T) {
	t.Parallel()
	moveAndLink := func(path string) {
		os.Rename(path, path+".moved")
		os.Symlink(path+".moved", path)
	}
	for name, spoil := range map[string]func(store string){
		"record is a directory": func(s string) { os.Remove(sessionFile(s, "x")); os.Mkdir(sessionFile(s, "x"), 0o755) },
		"record is a link":      func(s string) { moveAndLink(sessionFile(s, "x")) },
		"entries is a link":     func(s string) { moveAndLink(filepath.Join(s, "entries")) },
		"log is a link":         func(s string) { moveAndLink(filepath.Join(s, "log.jsonl")) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := laidStore(t, true)
			spoil(store)
			before := treeOf(store)
			if _, err := Append(store, "x", "e2", "more words", "", benchNow, PublishManual); err == nil {
				t.Fatal("the append was not refused")
			}
			if after := treeOf(store); after != before {
				t.Fatalf("a refused append stored something:\n%s\n%s", before, after)
			}
		})
	}
}

// SessionSource reads a bench header through the same check as every other
// read: a link is refused, and a header that exists and cannot be read is an
// error naming the record, never "no source".
func TestSessionSourceRefusesALinkAndAnUnreadableHeader(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	header := "# Cairn s1\n\nSession s1 opened 2026-09-29T08:00:00Z\nSource: marker\n"
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(outside, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(benchFile(store, "ok"), []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, benchFile(store, "leak")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if got, err := SessionSource(store, "ok"); err != nil || got != "marker" {
		t.Fatalf("a record in the store: %q %v", got, err)
	}
	if got, err := SessionSource(store, "leak"); err == nil || got != "" || !strings.Contains(err.Error(), "does not follow links") {
		t.Fatalf("a link gave its source %q, %v", got, err)
	}
	if got, err := SessionSource(store, "absent"); err != nil || got != "" {
		t.Fatalf("no record at all is no source: %q %v", got, err)
	}
	locked := benchFile(store, "locked")
	if err := os.WriteFile(locked, []byte(header), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(locked); err == nil {
		f.Close()
		t.Skip("mode 000 stays readable here (root or a lax file system)")
	}
	if got, err := SessionSource(store, "locked"); err == nil || got != "" || !strings.Contains(err.Error(), locked) {
		t.Fatalf("an unreadable header must be an error naming the record: %q %v", got, err)
	}
}

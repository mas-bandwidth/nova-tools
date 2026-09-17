// Red test for nova-tools #248: nova-cairn optional checkpoint mechanics
// without imposing a memory lifecycle.
//
// The friend's exact words must survive byte-for-byte with a real clock stamp
// and stable identifiers; a retried append must not duplicate; local
// persistence and remote publication are reported separately; an interrupted
// append must be recoverable without touching other writers' work.
package cairn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendKeepsExactProseAndReportsPersistenceSeparately(t *testing.T) {
	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	if err := Open(store, "sess-1", "bench-a/session-7", now, "manual"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	prose := "the friend's chosen words — \"as above\" is banned, \"café — 日本語\" stays byte-exact\nsecond line"
	res, err := Append(store, "sess-1", "e-1", prose, "bench-a/session-7#L3", now, "manual")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !res.Persisted {
		t.Fatalf("Append must report persisted=true once the note is fsync-durable, got %+v", res)
	}
	if res.Published {
		t.Fatalf("local persistence must not imply remote publication, got %+v", res)
	}
	got, err := EntryText(store, "sess-1", "e-1")
	if err != nil {
		t.Fatalf("EntryText: %v", err)
	}
	if got != prose {
		t.Fatalf("prose mangled:\n got %q\nwant %q", got, prose)
	}
	rc, err := Receipt(store, "sess-1", "e-1")
	if err != nil {
		t.Fatalf("Receipt: %v", err)
	}
	if !rc.Persisted || rc.Published {
		t.Fatalf("receipt must repeat persisted=true published=false, got %+v", rc)
	}
	if !rc.Stamp.Equal(now) {
		t.Fatalf("receipt stamp = %v, want the real clock stamp %v", rc.Stamp, now)
	}
}

func TestDuplicateAppendIsIdempotentAndConflictingEntryRefused(t *testing.T) {
	store := t.TempDir()
	now := time.Now().UTC()
	if err := Open(store, "s", "src", now, "never"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Append(store, "s", "e", "same words", "src", now, "never"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	dup, err := Append(store, "s", "e", "same words", "src", now, "never")
	if err != nil {
		t.Fatalf("retry of the same request must succeed, got %v", err)
	}
	if !dup.Duplicate {
		t.Fatalf("retry must report duplicate=true, got %+v", dup)
	}
	if _, err := Append(store, "s", "e", "DIFFERENT words", "src", now, "never"); err == nil {
		t.Fatalf("same entry id with different prose must be refused, not duplicated")
	}
	rows, _, err := Index(store, "", 0)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("duplicate retry left %d index rows, want 1", len(rows))
	}
}

func TestInterruptedAppendRecoversAndPreservesOtherWriters(t *testing.T) {
	store := t.TempDir()
	now := time.Now().UTC()
	if err := Open(store, "s", "src", now, "never"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Append(store, "s", "other", "other writer's note", "src", now, "never"); err != nil {
		t.Fatalf("Append other: %v", err)
	}
	// Simulate a crash between temp write and rename: a stale partial file.
	edir := filepath.Join(store, "entries", "s")
	if err := os.MkdirAll(edir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(edir, "mine.json.tmp"), []byte("{partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Append(store, "s", "mine", "my note after the crash", "src", now, "never")
	if err != nil {
		t.Fatalf("retry after interruption must succeed, got %v", err)
	}
	if !res.Persisted || res.Published {
		t.Fatalf("recovered append must report persisted=true published=false, got %+v", res)
	}
	if got, _ := EntryText(store, "s", "other"); got != "other writer's note" {
		t.Fatalf("other writer's entry mangled to %q", got)
	}
	rows, _, err := Index(store, "", 0)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("index holds %d rows, want 2 (partial tmp must never index)", len(rows))
	}
}

func TestOfflineAppendSucceedsWithPublicationPending(t *testing.T) {
	store := t.TempDir()
	now := time.Now().UTC()
	if err := Open(store, "s", "src", now, "deferred"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	res, err := Append(store, "s", "e", "offline note", "", now, "deferred")
	if err != nil {
		t.Fatalf("offline append must succeed locally, got %v", err)
	}
	if !res.Persisted || res.Published {
		t.Fatalf("offline append is durable-but-unpublished: persisted=true published=false, got %+v", res)
	}
}

func TestOpenConcurrentRecordsAndAlternateHeaders(t *testing.T) {
	store := t.TempDir()
	now := time.Now().UTC()
	for _, s := range []string{"alpha", "beta"} {
		if err := Open(store, s, "src", now, "never"); err != nil {
			t.Fatalf("Open %s: %v", s, s)
		}
		if _, err := Append(store, s, "e", "note in "+s, "src", now, "never"); err != nil {
			t.Fatalf("Append %s: %v", s, err)
		}
	}
	// A friend with different header conventions rewrites the session file's
	// header by hand; the entries and the index must survive it.
	sessFile := filepath.Join(store, "sessions", "alpha.md")
	raw, err := os.ReadFile(sessFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	lines[0] = "# My own heading convention — tool must not care"
	if err := os.WriteFile(sessFile, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := EntryText(store, "alpha", "e")
	if err != nil {
		t.Fatalf("EntryText after header rewrite: %v", err)
	}
	if got != "note in alpha" {
		t.Fatalf("entry mangled to %q", got)
	}
	rows, total, err := Index(store, "", 0)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("index = %d rows total %d, want 2 across both concurrent records", len(rows), total)
	}
	led := Coverage(store)
	if led.Sessions != 2 || led.Entries != 2 {
		t.Fatalf("coverage ledger = %+v, want 2 sessions and 2 entries", led)
	}
}

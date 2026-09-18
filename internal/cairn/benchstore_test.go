// Red test for the bench store: a directory of markdown files, one per
// session, kept by hand.
//
// The hurt (2026-09-18, the Studio): the documented sequence
//
//	nova-cairn append --store cairns --session b9395d11 --entry <id> \
//	  --publish manual --file -
//
// refused with `no such session "b9395d11"; open first` although
// `cairns/b9395d11.md` was right there, 370 lines of it, appended by hand all
// day. The tool looks only under `sessions/<id>.md`; the bench keeps the file
// directly under the store. Two costs, both real: the refusal was FALSE (the
// record exists), and it named no verb a friend could run — `open first` on a
// store that already holds the session would have written a SECOND record
// under sessions/ and split the session in two.
//
// So: `append` and `open` read the store's own shape. A file per session under
// the store is a record like any other; the append lands as a dated section in
// the file's own shape, and nothing else in the directory is touched. The
// fixture below is the first 60 lines of the real cairn, copied on the day.
package cairn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchFixture is today's real cairn, copied into testdata. A test that proves
// an append lands "in the file's own shape" has to read the real shape.
const benchFixture = "testdata/bench-b9395d11.md"

// benchStore lays the fixture down as the bench keeps it: <store>/<session>.md,
// no sessions/ directory, no entries/, no log.
func benchStore(t *testing.T, session string) (store, file string, before []byte) {
	t.Helper()
	raw, err := os.ReadFile(benchFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	store = t.TempDir()
	file = filepath.Join(store, session+".md")
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatalf("write bench file: %v", err)
	}
	return store, file, raw
}

func TestAppendLandsInTheBenchFileStore(t *testing.T) {
	const session = "b9395d11"
	store, file, before := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	prose := "## 14:05Z beat: the lane class landed\n- two PRs, red first; the tails on the card."

	res, err := Append(store, session, "beat-1405", prose, "transcript#L1", now, "manual")
	if err != nil {
		t.Fatalf("Append into the bench store: %v", err)
	}
	if !res.Persisted || res.Published {
		t.Fatalf("append must report persisted=true published=false, got %+v", res)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(raw)
	if !strings.HasPrefix(got, string(before)) {
		t.Fatalf("the hand-kept record was rewritten; an append only adds to the end")
	}
	head := "## " + now.Format(time.RFC3339) + " — beat-1405"
	if !strings.Contains(got, head) {
		t.Fatalf("no dated section %q in the file; the append must land in the file's own shape:\n%s", head, tail(got, 400))
	}
	if !strings.Contains(got, prose) {
		t.Fatalf("the friend's words are not in the record byte-for-byte:\n%s", tail(got, 400))
	}
	if strings.Contains(got[strings.Index(got, head):], "\n\n\n") {
		t.Fatalf("the appended section is not in the file's shape (one blank line between sections):\n%q", tail(got, 200))
	}
	// Nothing else appears beside a hand-kept store: no sessions/, no entries/.
	for _, unwanted := range []string{"sessions", "entries", "log.jsonl"} {
		if _, err := os.Stat(filepath.Join(store, unwanted)); err == nil {
			t.Errorf("append created %s/ beside a hand-kept store; the file IS the record", unwanted)
		}
	}
}

func TestAppendToTheBenchFileRetriesAsADuplicate(t *testing.T) {
	const session = "b9395d11"
	store, file, _ := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	prose := "the same words, twice"

	if _, err := Append(store, session, "e-1", prose, "src", now, "manual"); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	once, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Append(store, session, "e-1", prose, "src", now.Add(time.Minute), "manual")
	if err != nil {
		t.Fatalf("retry Append: %v", err)
	}
	if !res.Duplicate {
		t.Errorf("a retry of the same id and the same words is duplicate=true, got %+v", res)
	}
	twice, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("the retry wrote a second section; a retry adds nothing")
	}
}

func TestAppendToTheBenchFileRefusesDifferentProseUnderTheSameID(t *testing.T) {
	const session = "b9395d11"
	store, _, _ := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	if _, err := Append(store, session, "e-1", "the first words", "src", now, "manual"); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	_, err := Append(store, session, "e-1", "different words", "src", now, "manual")
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("different prose under a used id must be a conflict, got %v", err)
	}
}

func TestOpenOnABenchFileIsANoOpAndNeverSplitsTheRecord(t *testing.T) {
	const session = "b9395d11"
	store, file, before := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)

	if err := Open(store, session, "src", now, "manual"); err != nil {
		t.Fatalf("Open on an existing bench record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "sessions", session+".md")); err == nil {
		t.Fatalf("open wrote a second record under sessions/; the session would be split in two")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(before) {
		t.Fatalf("open rewrote the hand-kept record; re-opening an open session is a no-op")
	}
}

func TestAppendWithNoRecordAnywhereNamesTheOpenVerb(t *testing.T) {
	store := t.TempDir()
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	_, err := Append(store, "nosuch", "e-1", "words", "src", now, "manual")
	if err == nil {
		t.Fatal("append into a store with no record must refuse")
	}
	msg := err.Error()
	for _, want := range []string{"nova-cairn open", "--store", "--session nosuch", "--publish"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must name the remedy verb whole; %q is missing from %q", want, msg)
		}
	}
}

func TestCoverageCountsTheBenchSessionFiles(t *testing.T) {
	store, _, _ := benchStore(t, "b9395d11")
	if got := Coverage(store).Sessions; got != 1 {
		t.Fatalf("Coverage counted %d sessions in a bench store holding one record; the ledger must not read zero over a store it can append to", got)
	}
}

// tail is the last n bytes of s, for a failure that names what it saw without
// printing a 370-line record.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

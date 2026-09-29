package cairn

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// nonRecords lays down, in a bench store that already holds a.md, one session
// path per thing that is not a record.
func nonRecords(t *testing.T) (store string, want map[string]string) {
	t.Helper()
	store = t.TempDir()
	if err := os.WriteFile(benchFile(store, "a"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(benchFile(store, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(store, "realdir")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"tolink": realDir, "dangling": filepath.Join(store, "nowhere", "zz.md")} {
		if err := os.Symlink(target, benchFile(store, name)); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}
	return store, map[string]string{
		"dir":      "a directory",
		"tolink":   "a symlink to a directory",
		"dangling": "a dangling symlink",
	}
}

// open reported OPEN OK over these, and the append after it refused "open
// first": a loop. Every verb now meets the same thing and says what it is.
func TestNonRegularRecordPathIsRefusedByEveryVerb(t *testing.T) {
	t.Parallel()
	store, want := nonRecords(t)
	before := listing(t, store)
	for id, found := range want {
		path := benchFile(store, id)
		if id == "dir" {
			// A directory named <id>.md never counts as a session file, so
			// the shape still reads the store as bench through a.md.
			if got, err := storeShape("open", store); got != shapeBench || err != nil {
				t.Fatalf("shape %v %v", got, err)
			}
		}
		checks := map[string]error{
			"open":    Open(store, id, "", benchNow, PublishManual),
			"append":  func() error { _, err := Append(store, id, "e", "w", "", benchNow, PublishManual); return err }(),
			"index":   func() error { _, _, err := Index(store, id, 0); return err }(),
			"receipt": func() error { _, err := Receipt(store, id, "e"); return err }(),
		}
		for verb, err := range checks {
			var nf *NotFoundError
			if err == nil || errors.As(err, &nf) || strings.Contains(err.Error(), "open first") {
				t.Errorf("%s %s: want a refusal naming what is there, got %v", verb, id, err)
				continue
			}
			for _, w := range []string{path, found, "move or remove it, or choose another session id", "cannot " + opPhrase(verb)} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%s %s: %q lacks %q", verb, id, err, w)
				}
			}
		}
	}
	if got := listing(t, store); !reflect.DeepEqual(got, before) {
		t.Fatalf("a refused verb changed the store: %v -> %v", before, got)
	}
}

// A dangling or directory symlink beside the sessions makes the store-wide
// index refuse, naming it, rather than skip a record it cannot read.
func TestIndexOfAStoreHoldingADanglingLinkRefusesNamingIt(t *testing.T) {
	t.Parallel()
	store, _ := nonRecords(t)
	_, _, err := Index(store, "", 0)
	if err == nil || !strings.Contains(err.Error(), "the session record") {
		t.Fatalf("index over a store with an unreadable record path: %v", err)
	}
}

func TestSymlinkToARegularFileIsARecord(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	real := filepath.Join(t.TempDir(), "real.md")
	if err := os.WriteFile(real, []byte("# real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, benchFile(store, "s1")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if got, err := storeShape("open", store); got != shapeBench || err != nil {
		t.Fatalf("a symlink named <id>.md is a session file: shape %v %v", got, err)
	}
	if err := Open(store, "s1", "", benchNow, PublishManual); err != nil {
		t.Fatalf("open on a symlinked record: %v", err)
	}
	if _, err := Append(store, "s1", "e1", "words", "", benchNow, PublishManual); err != nil {
		t.Fatalf("append through a symlinked record: %v", err)
	}
	raw, _ := os.ReadFile(real)
	if !strings.Contains(string(raw), "— e1") {
		t.Fatalf("the append did not land in the linked file: %q", raw)
	}
	if fi, _ := os.Lstat(benchFile(store, "s1")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
}

// A symlink named like an own-shape marker is a marker, whatever it points at.
func TestSymlinkedOwnShapeMarkersCountAsMarkers(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"sessions", "entries", "log.jsonl"} {
		store := t.TempDir()
		if err := os.WriteFile(benchFile(store, "hand"), []byte("# hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := t.TempDir()
		if name == "log.jsonl" {
			target = filepath.Join(target, "log")
			if err := os.WriteFile(target, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(store, name)); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
		got, err := storeShape("open", store)
		var me *MixedShapeError
		if got != shapeMixed || !errors.As(err, &me) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: a symlinked marker beside a session file must make a mixed store, got %v %v", name, got, err)
		}
		// Dangling, too.
		store2 := t.TempDir()
		if err := os.WriteFile(benchFile(store2, "hand"), []byte("# hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(store2, "nowhere"), filepath.Join(store2, name)); err != nil {
			t.Skip(err)
		}
		if got, _ := storeShape("open", store2); got != shapeMixed {
			t.Errorf("%s dangling: shape %v, want mixed", name, got)
		}
	}
}

// foldsCase probes the file system the test runs on.
func foldsCase(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Probe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "probe"))
	return err == nil
}

// Only the exact ".md" name is a session file. A file named s1.MD is not one on
// any file system; where the disk folds case, a verb addressing s1 must not
// find it through the fold, so open, append, receipt and index --session refuse
// and none writes through it. Where the disk keeps case, s1.md is simply a
// different name and is created. The test states the outcome for both.
func TestCaseFoldedRecordNamesAreNotSessionFiles(t *testing.T) {
	t.Parallel()
	folds := foldsCase(t)
	store := t.TempDir()
	if err := os.WriteFile(benchFile(store, "a"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(store, "s1.MD")
	if err := os.WriteFile(other, []byte("# other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(other)

	// The store-wide readers agree that s1.MD is not a session.
	rows, total, err := Index(store, "", 0)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("index lists %v %d %v", rows, total, err)
	}
	if got := Coverage(store).Sessions; got != 1 {
		t.Fatalf("coverage counts %d sessions, want 1 (a.md only)", got)
	}

	if folds {
		listing0 := listing(t, store)
		checks := map[string]error{
			"open":    Open(store, "s1", "", benchNow, PublishManual),
			"append":  func() error { _, err := Append(store, "s1", "e", "w", "", benchNow, PublishManual); return err }(),
			"index":   func() error { _, _, err := Index(store, "s1", 0); return err }(),
			"receipt": func() error { _, err := Receipt(store, "s1", "e"); return err }(),
		}
		for verb, err := range checks {
			var rp *RecordPathError
			if !errors.As(err, &rp) || !strings.Contains(err.Error(), "only by letter case") || !strings.Contains(err.Error(), "s1.MD") {
				t.Errorf("%s: want a refusal naming the case-folded match, got %v", verb, err)
			}
		}
		if got := listing(t, store); !reflect.DeepEqual(got, listing0) {
			t.Fatalf("a refused verb changed the store: %v -> %v", listing0, got)
		}
	} else {
		if err := Open(store, "s1", "", benchNow, PublishManual); err != nil {
			t.Fatalf("open on a case-keeping disk: %v", err)
		}
		if _, err := os.Stat(benchFile(store, "s1")); err != nil {
			t.Fatalf("s1.md was not created beside s1.MD: %v", err)
		}
	}
	if after, _ := os.ReadFile(other); string(after) != string(before) {
		t.Fatalf("s1.MD was written through: %q", after)
	}

	// The same holds for an id that differs from an existing session by case.
	if folds {
		if _, err := Append(store, "A", "e", "w", "", benchNow, PublishManual); err == nil || !strings.Contains(err.Error(), "only by letter case") {
			t.Fatalf("append to A beside a.md: %v", err)
		}
	}
}

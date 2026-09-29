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

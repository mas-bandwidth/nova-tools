package cairn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkText is what every refusal of a symbolic link says.
var linkText = []string{"is a symbolic link", "does not follow links", "ls -ld -- "}

// layOwn lays down by hand, with no sync, what an open and an append of entry e1
// leave in the tool's own shape: the session file, the entry file, the log.
func layOwn(t *testing.T, store, session string) {
	t.Helper()
	entry := `{"session":"` + session + `","id":"e1","stamp":"2026-09-29T08:00:00Z","source":"","publish":"manual","text":"words"}`
	for path, body := range map[string]string{
		sessionFile(store, session):       "# cairn " + session + "\n\nENTRY e1 2026-09-29T08:00:00Z\n",
		entryPath(store, session, "e1"):   entry,
		filepath.Join(store, "log.jsonl"): `{"event":"open","session":"` + session + `"}` + "\n" + entry + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644); err != nil {
			t.Fatal(err)
		} else {
			f.WriteString(body)
			f.Close()
		}
	}
}

// The store the two shapes share for these tests, laid down by hand: a real
// session x and an entry e1 in it.
func laidStore(t *testing.T, own bool) string {
	t.Helper()
	store := t.TempDir()
	if own {
		layOwn(t, store, "x")
	} else if err := os.WriteFile(benchFile(store, "x"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return store
}

// treeOf is every path under root with the bytes of every file, so two calls
// compare whole trees.
func treeOf(root string) (out string) {
	filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		raw, _ := os.ReadFile(p)
		out += p + "=" + string(raw) + "\n"
		return nil
	})
	return out
}

// nova-cairn does not follow a symbolic link: not at the store directory's own
// name, sessions/, entries/, an entry directory, log.jsonl or a record, for a
// read or a write, in either shape. Each refusal is the one text, names the
// path, and nothing is written to where the link leads.
func TestNoVerbFollowsASymbolicLink(t *testing.T) {
	t.Parallel()
	for name, l := range map[string]struct {
		own  bool
		path func(store string) string
	}{
		"bench record":    {false, func(s string) string { return benchFile(s, "x") }},
		"bench store":     {false, func(s string) string { return s }},
		"own record":      {true, func(s string) string { return sessionFile(s, "x") }},
		"sessions":        {true, func(s string) string { return filepath.Join(s, "sessions") }},
		"entries":         {true, func(s string) string { return filepath.Join(s, "entries") }},
		"entry directory": {true, func(s string) string { return filepath.Join(s, "entries", "x") }},
		"log":             {true, func(s string) string { return filepath.Join(s, "log.jsonl") }},
		"own store":       {true, func(s string) string { return s }},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := laidStore(t, l.own)
			path := l.path(store)
			target := t.TempDir() // where the link leads: a directory, or for a file a file inside it
			if fi, _ := os.Lstat(path); !fi.IsDir() {
				target = filepath.Join(target, "elsewhere")
				os.WriteFile(target, []byte("precious\n"), 0o644)
			}
			if path == store {
				store = filepath.Join(t.TempDir(), "linked")
				path = store
				if err := os.Symlink(l.path(laidStore(t, l.own)), store); err != nil {
					t.Skipf("no symlinks here: %v", err)
				}
			} else if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
				t.Fatal(err)
			} else if err := os.Symlink(target, path); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			before := treeOf(filepath.Dir(target))
			_, appended := Append(store, "x", "e2", "w", "", benchNow, PublishManual)
			_, indexed := IndexAll(store, "", 0)
			_, received := Receipt(store, "x", "e1")
			for verb, err := range map[string]error{"open": Open(store, "x", "", benchNow, PublishManual), "append": appended, "index": indexed, "receipt": received} {
				if err == nil {
					continue // a verb the link does not stand in the way of
				}
				for _, w := range append([]string{path, "cannot " + opPhrase(verb)}, linkText...) {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("%s: %q lacks %q", verb, err, w)
					}
				}
			}
			if appended == nil {
				t.Errorf("append followed the link at %s", path)
			}
			if after := treeOf(filepath.Dir(target)); after != before {
				t.Fatalf("something was written where the link leads:\n%s\n%s", before, after)
			}
		})
	}
}

// The store as given may be named any way: relative or absolute, in any letter
// case the disk folds. The answer is the same, because nothing is resolved.
func TestTheSpellingOfTheStoreDoesNotChangeTheAnswer(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "St")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# t\n\n## 2026-09-29T08:00:00Z — e1\n\nwords\n"
	if err := os.WriteFile(filepath.Join(store, "target.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// One link written absolute, one relative.
	if err := os.Symlink(filepath.Join(store, "target.md"), benchFile(store, "abslink")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink("target.md", benchFile(store, "rellink")); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, store)
	if err != nil {
		t.Skipf("no relative spelling: %v", err)
	}
	spelled := map[string]string{"absolute": store, "relative": rel}
	if foldsCase(t) {
		spelled["upper"] = filepath.Join(filepath.Dir(store), "ST")
		spelled["lower"] = filepath.Join(filepath.Dir(store), "st")
	}
	answer := func(s string) string {
		res, err := IndexAll(s, "", 0)
		var flagged []string
		for _, f := range res.Flagged {
			flagged = append(flagged, f.Session)
		}
		_, appended := Append(s, "abslink", "e2", "more", "", benchNow, PublishManual)
		refused := appended != nil && strings.Contains(appended.Error(), "does not follow links")
		return fmt.Sprintf("sessions=%d entries=%d flagged=%v err=%v link-refused=%v", res.Sessions, res.Total, flagged, err, refused)
	}
	want := answer(store)
	if !strings.Contains(want, "link-refused=true") || !strings.Contains(want, "flagged=[abslink rellink]") {
		t.Fatalf("the reference answer does not refuse the links: %s", want)
	}
	for name, s := range spelled {
		if got := answer(s); got != want {
			t.Errorf("%s spelling %q answers differently:\n %s\n %s", name, s, got, want)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

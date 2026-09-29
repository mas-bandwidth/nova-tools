package cairn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// relativeTo spells dir relative to the working directory, without changing
// it, so a test can name one store two ways and stay parallel.
func relativeTo(t *testing.T, dir string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, dir)
	if err != nil || filepath.IsAbs(rel) {
		t.Skipf("no relative spelling of %q from %q: %v", dir, wd, err)
	}
	return rel
}

// The answer never depends on how --store is spelled. A link whose target is
// written absolute, inside the store, is a record when the store is named
// relative just as when it is named absolute; and index counts and flags the
// same sessions either way.
func TestTheSpellingOfTheStoreDoesNotChangeTheAnswer(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	body := "# t\n\n## 2026-09-29T08:00:00Z — e1\n\nwords\n"
	if err := os.WriteFile(filepath.Join(store, "target.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.EvalSymlinks(store)
	if err != nil {
		t.Fatal(err)
	}
	// One link written absolute, one relative, one to a target outside.
	if err := os.Symlink(filepath.Join(abs, "target.md"), benchFile(store, "abslink")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink("target.md", benchFile(store, "rellink")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, benchFile(store, "leak")); err != nil {
		t.Fatal(err)
	}
	spelled := map[string]string{"absolute": store, "relative": relativeTo(t, store)}
	type answer struct {
		sessions, total int
		flagged         string
		abslink, leak   string
	}
	got := map[string]answer{}
	// Index under both spellings first: the appends below add an entry.
	for name, s := range spelled {
		res, err := IndexAll(s, "", 0)
		if err != nil {
			t.Fatalf("%s: index: %v", name, err)
		}
		var flagged []string
		for _, f := range res.Flagged {
			flagged = append(flagged, f.Session)
		}
		got[name] = answer{sessions: res.Sessions, total: res.Total, flagged: strings.Join(flagged, ",")}
	}
	for name, s := range spelled {
		a := got[name]
		_, a1 := Append(s, "abslink", "e2", "more", "", benchNow, PublishManual)
		_, a2 := Append(s, "leak", "e2", "more", "", benchNow, PublishManual)
		a.abslink, a.leak = errText(a1), errText(a2)
		got[name] = a
	}
	if got["absolute"].flagged != "leak" || got["absolute"].abslink != "" || !strings.Contains(got["absolute"].leak, "resolves outside the store") {
		t.Fatalf("the absolute spelling is not the reference answer: %+v", got["absolute"])
	}
	// The error text names the path as spelled; the outcome is what must match.
	r, a := got["relative"], got["absolute"]
	if r.sessions != a.sessions || r.total != a.total || r.flagged != a.flagged ||
		(r.abslink == "") != (a.abslink == "") || strings.Contains(r.leak, "resolves outside the store") != strings.Contains(a.leak, "resolves outside the store") {
		t.Fatalf("the answer depends on the spelling of --store:\n absolute %+v\n relative %+v", a, r)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

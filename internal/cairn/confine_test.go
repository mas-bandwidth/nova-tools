package cairn

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// A record link re-pointed between the check and the open must never carry a
// write outside the store. A background writer swaps the session's link
// between a file inside the store and one outside it while appends run; the
// outside file is never touched, whichever way each append ends.
func TestAppendNeverWritesOutsideTheStoreWhileTheLinkIsSwapped(t *testing.T) {
	for _, shape := range []string{"bench", "own"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			linkSwapped(t, shape == "own")
		})
	}
}

func linkSwapped(t *testing.T, own bool) {
	store := t.TempDir()
	inside := filepath.Join(store, "in.md")
	if own {
		// A top-level .md would make the own store a mixed one.
		inside = filepath.Join(store, "sessions", "in.md")
		if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.md")
	const untouched = "# outside\n"
	for path, body := range map[string]string{inside: "# in\n", outside: untouched} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := benchFile(store, "x")
	if own {
		link = sessionFile(store, "x")
	}
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		targets := [2]string{outside, inside}
		tmp := filepath.Join(filepath.Dir(link), "swap.tmp")
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// An atomic re-point: a new link renamed over the old one.
			os.Remove(tmp)
			if os.Symlink(targets[i%2], tmp) == nil {
				os.Rename(tmp, link)
			}
		}
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	landed := 0
	for i := 0; time.Now().Before(deadline); i++ {
		if _, err := Append(store, "x", "e"+strconv.Itoa(i), "words", "", benchNow, PublishManual); err == nil {
			landed++
		}
		if raw, _ := os.ReadFile(outside); string(raw) != untouched {
			break
		}
	}
	close(stop)
	<-done
	if raw, _ := os.ReadFile(outside); string(raw) != untouched {
		t.Fatalf("an append wrote outside the store (%d appends landed): %q", landed, raw)
	}
}

// The check made on the open file: the descriptor held is the file the name
// leads to, inside the store. A descriptor on some other file, or on a file
// outside, is refused whatever the name says.
func TestAnOpenedRecordMustBeTheFileTheNameLeadsToInsideTheStore(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	other := t.TempDir()
	for path, body := range map[string]string{benchFile(store, "a"): "# a\n", benchFile(store, "b"): "# b\n", filepath.Join(other, "o.md"): "# o\n"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	open := func(path string) *os.File {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	d := newDirs()
	if err := holdsRecord(d, "append", store, benchFile(store, "a"), open(benchFile(store, "a"))); err != nil {
		t.Fatalf("the file the name leads to: %v", err)
	}
	for name, f := range map[string]*os.File{"another file in the store": open(benchFile(store, "b")), "a file outside the store": open(filepath.Join(other, "o.md"))} {
		err := holdsRecord(d, "append", store, benchFile(store, "a"), f)
		if err == nil || !strings.Contains(err.Error(), "is not the file that was checked") || !strings.Contains(err.Error(), "cannot append an entry") {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
	// The name itself leading outside is refused for the file it leads to.
	if err := os.Symlink(filepath.Join(other, "o.md"), benchFile(store, "leak")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if _, err := readRecord(d, "index", store, benchFile(store, "leak")); err == nil || !strings.Contains(err.Error(), "is not the file that was checked") {
		t.Fatalf("reading a record that leads outside: %v", err)
	}
}

// The check on the open file is the check recordState makes on the path, no
// stricter: a store whose sessions/ directory is itself a link to a directory
// elsewhere keeps working, because only the record's own name is judged.
func TestOwnStoreWithALinkedSessionsDirectoryStillAppends(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(store, "sessions")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "s1.md"), []byte("# s1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(store, "s1", "e1", "words", "", benchNow, PublishManual); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(elsewhere, "s1.md"))
	if err != nil || !strings.Contains(string(raw), "ENTRY e1 ") {
		t.Fatalf("the pointer line did not land in the linked directory: %q %v", raw, err)
	}
}

// SessionSource reads the header through the same check as every other read of
// a record: a record that leads outside the store gives no source.
func TestSessionSourceOfABenchRecordIsConfined(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	header := "# Cairn s1\n\nSession s1 opened 2026-09-29T08:00:00Z\nSource: outside-marker\n"
	if err := os.WriteFile(outside, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(benchFile(store, "ok"), []byte(strings.Replace(header, "outside-marker", "inside-marker", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, benchFile(store, "leak")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if got, err := SessionSource(store, "ok"); err != nil || got != "inside-marker" {
		t.Fatalf("a record in the store: %q %v", got, err)
	}
	if got, err := SessionSource(store, "leak"); err == nil || got != "" {
		t.Fatalf("a record that leads outside the store gave its source %q, %v", got, err)
	}
	if got, err := SessionSource(store, "absent"); err != nil || got != "" {
		t.Fatalf("no record at all is no source: %q %v", got, err)
	}
}

// An unreadable header is an error naming the record, never "no source": the
// CLI would otherwise report a successful open with source=- over a pointer
// that open wrote.
func TestAnUnreadableBenchHeaderIsAnErrorNotNoSource(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	locked := benchFile(store, "locked")
	if err := os.WriteFile(locked, []byte("# Cairn s1\n\nSession s1 opened 2026-09-29T08:00:00Z\nSource: x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(locked); err == nil {
		f.Close()
		t.Skip("mode 000 stays readable here (root or a lax file system)")
	}
	got, err := SessionSource(store, "locked")
	if err == nil || got != "" || !strings.Contains(err.Error(), locked) {
		t.Fatalf("an unreadable header must be an error naming the record: %q %v", got, err)
	}
}

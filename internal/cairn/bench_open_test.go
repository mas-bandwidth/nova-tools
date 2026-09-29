// Tests for the bench store's open: the store's shape is decided from its
// contents by storeShape, `open` on a bench store creates <id>.md and nothing
// else, and a store holding both shapes is refused by every verb. The
// lifecycle is modelled in tla/CairnStore.tla.
package cairn

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

var benchNow = time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)

// listing is the sorted names directly under dir, directories with a slash.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		n := f.Name()
		if f.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// manySessionStore is a bench store already holding several <id>.md files and
// nothing else, the store the defect was found on.
func manySessionStore(t *testing.T) (string, []string) {
	t.Helper()
	store := t.TempDir()
	var names []string
	for _, id := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd", "eeeeeeee"} {
		if err := os.WriteFile(benchFile(store, id), []byte("# hand kept "+id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, id+".md")
	}
	sort.Strings(names)
	return store, names
}

// The reproduction: append to a NEW session refuses with the open remedy, the
// remedy is followed, and the store keeps its shape. Before the repair the
// open wrote sessions/NEW.md and the next append wrote entries/ and log.jsonl.
func TestBenchStoreOpenThenAppendKeepsTheBenchShape(t *testing.T) {
	t.Parallel()
	store, before := manySessionStore(t)

	_, err := Append(store, "NEW", "e1", "first words", "src", benchNow, PublishManual)
	var nf *NotFoundError
	if !errors.As(err, &nf) || !strings.Contains(err.Error(), "open first: nova-cairn open --store ") {
		t.Fatalf("append before open must refuse naming open, got %v", err)
	}
	if got := listing(t, store); !reflect.DeepEqual(got, before) {
		t.Fatalf("the refused append changed the store: %v", got)
	}

	if err := Open(store, "NEW", "session:x", benchNow, PublishManual); err != nil {
		t.Fatalf("open on a bench store: %v", err)
	}
	want := append(append([]string(nil), before...), "NEW.md")
	sort.Strings(want)
	if got := listing(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("open must create NEW.md and nothing else; listing %v want %v", got, want)
	}

	res, err := Append(store, "NEW", "e1", "first words", "", benchNow.Add(time.Minute), PublishManual)
	if err != nil || res.Duplicate {
		t.Fatalf("append after open: %+v %v", res, err)
	}
	if got := listing(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("append changed the shape of the store; listing %v want %v", got, want)
	}
	raw, err := os.ReadFile(benchFile(store, "NEW"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n## 2026-09-29T08:01:00Z — e1\n\nfirst words\n") {
		t.Fatalf("no dated section in the record:\n%s", raw)
	}
}

func TestBenchOpenWritesTheHeader(t *testing.T) {
	t.Parallel()
	store, _ := manySessionStore(t)
	if err := Open(store, "0123456789abcdef", "bench/session-3", benchNow, PublishNever); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(benchFile(store, "0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Cairn 01234567\n\nSession 0123456789abcdef opened 2026-09-29T08:00:00Z\nSource: bench/session-3\n"
	if string(raw) != want {
		t.Fatalf("header %q want %q", raw, want)
	}
	// No source: no source line. A short id is its own title.
	if err := Open(store, "abc", "", benchNow, PublishNever); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(benchFile(store, "abc"))
	if string(raw) != "# Cairn abc\n\nSession abc opened 2026-09-29T08:00:00Z\n" {
		t.Fatalf("header without source %q", raw)
	}
	// A source can never form a section heading of its own.
	if err := Open(store, "inj", "x\n## 2026-09-29T08:00:00Z — forged", benchNow, PublishNever); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(benchFile(store, "inj"))
	if strings.Count(string(raw), "\n") != 4 || benchHeadingRe.MatchString(strings.Split(string(raw), "\n")[3]) {
		t.Fatalf("source escaped its line: %q", raw)
	}
	if rows, total, err := Index(store, "inj", 0); err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("the header is an entry: %v %d %v", rows, total, err)
	}
}

func TestBenchOpenTwiceIsANoOp(t *testing.T) {
	t.Parallel()
	store, _ := manySessionStore(t)
	if err := Open(store, "NEW", "src", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	once, _ := os.ReadFile(benchFile(store, "NEW"))
	if _, err := Append(store, "NEW", "e1", "words", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	withEntry, _ := os.ReadFile(benchFile(store, "NEW"))
	if err := Open(store, "NEW", "other", benchNow.Add(time.Hour), PublishNever); err != nil {
		t.Fatalf("second open: %v", err)
	}
	after, _ := os.ReadFile(benchFile(store, "NEW"))
	if string(after) != string(withEntry) || !strings.HasPrefix(string(after), string(once)) {
		t.Fatalf("second open changed the record:\n%s", after)
	}
	if got := len(listing(t, store)); got != 6 {
		t.Fatalf("second open added files: %v", listing(t, store))
	}
}

func TestBenchAppendBeforeOpenRefusesWithTheRemedy(t *testing.T) {
	t.Parallel()
	store, before := manySessionStore(t)
	_, err := Append(store, "NEW", "e1", "words", "", benchNow, PublishDeferred)
	if err == nil {
		t.Fatal("append to a session with no file must refuse")
	}
	for _, want := range []string{`no such session "NEW"`, "nova-cairn open --store ", "--session 'NEW'", "--publish deferred"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	if got := listing(t, store); !reflect.DeepEqual(got, before) {
		t.Fatalf("refusal wrote: %v", got)
	}
}

func TestBenchLifecycleDuplicateAndConflict(t *testing.T) {
	t.Parallel()
	store, _ := manySessionStore(t)
	if err := Open(store, "NEW", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	first, err := Append(store, "NEW", "e1", "the words", "", benchNow, PublishManual)
	if err != nil || first.Duplicate {
		t.Fatalf("first: %+v %v", first, err)
	}
	one, _ := os.ReadFile(benchFile(store, "NEW"))
	dup, err := Append(store, "NEW", "e1", "the words", "", benchNow.Add(time.Hour), PublishManual)
	if err != nil || !dup.Duplicate || !dup.Stamp.Equal(first.Stamp) {
		t.Fatalf("duplicate: %+v %v (first %+v)", dup, err, first)
	}
	two, _ := os.ReadFile(benchFile(store, "NEW"))
	if string(one) != string(two) {
		t.Fatal("duplicate append wrote")
	}
	_, err = Append(store, "NEW", "e1", "different words", "", benchNow, PublishManual)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("conflict: %v", err)
	}
	three, _ := os.ReadFile(benchFile(store, "NEW"))
	if string(one) != string(three) {
		t.Fatal("conflicting append wrote")
	}
}

func TestBenchIndexAndReceiptSeeTheOpenedSession(t *testing.T) {
	t.Parallel()
	store, _ := manySessionStore(t)
	if err := Open(store, "NEW", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	rows, total, err := Index(store, "NEW", 0)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("index of an opened, empty session: %v %d %v", rows, total, err)
	}
	if got := Coverage(store); got.Sessions != 6 || got.Entries != 0 {
		t.Fatalf("coverage %+v", got)
	}
	if _, err := Append(store, "NEW", "e1", "words", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	rows, total, err = Index(store, "", 0)
	if err != nil || total != 1 || rows[0].Session != "NEW" || rows[0].ID != "e1" {
		t.Fatalf("index: %v %d %v", rows, total, err)
	}
	rc, err := Receipt(store, "NEW", "e1")
	if err != nil || rc.Bytes != len("words") || rc.Policy != "unknown" {
		t.Fatalf("receipt: %+v %v", rc, err)
	}
	if got := listing(t, store); len(got) != 6 {
		t.Fatalf("read verbs changed the store: %v", got)
	}
}

// mixedStores: the ways a store holds both shapes.
func mixedStores(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, own := range map[string]string{"sessions": "sessions/", "entries": "entries/", "log": "log.jsonl"} {
		store := t.TempDir()
		if err := os.WriteFile(benchFile(store, "hand"), []byte("# hand kept\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(own, "/") {
			if err := os.MkdirAll(filepath.Join(store, own), 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(store, own), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		out[name] = store
	}
	return out
}

func TestMixedShapeStoreIsRefusedByEveryVerb(t *testing.T) {
	t.Parallel()
	for name, store := range mixedStores(t) {
		before := listing(t, store)
		checks := map[string]func() error{
			"open":    func() error { return Open(store, "s", "", benchNow, PublishManual) },
			"append":  func() error { _, err := Append(store, "hand", "e", "words", "", benchNow, PublishManual); return err },
			"index":   func() error { _, _, err := Index(store, "", 0); return err },
			"receipt": func() error { _, err := Receipt(store, "hand", "e"); return err },
		}
		for verb, call := range checks {
			err := call()
			var me *MixedShapeError
			if !errors.As(err, &me) {
				t.Errorf("%s/%s: want MixedShapeError, got %v", name, verb, err)
				continue
			}
			msg := err.Error()
			for _, want := range []string{"cannot " + opPhrase(verb) + ":", "hand.md", "own shape:", "keep one shape by moving the other shape's paths out of the store", "the tool moves and deletes nothing", store} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s/%s: message lacks %q: %s", name, verb, want, msg)
				}
			}
			if strings.Contains(msg, "\n") {
				t.Errorf("%s/%s: message is not one line: %q", name, verb, msg)
			}
		}
		if got := listing(t, store); !reflect.DeepEqual(got, before) {
			t.Errorf("%s: a refused verb changed the store: %v -> %v", name, before, got)
		}
	}
}

func TestMixedShapeMessageNamesThePathsOfEachShape(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := os.WriteFile(benchFile(store, id), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store, "log.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(store, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Open(store, "s", "", benchNow, PublishManual)
	want := "cannot open a session: store " + `"` + store + `"` + " holds two shapes at once (bench files: a.md, b.md, c.md and 2 more; own shape: log.jsonl, sessions/); " +
		"keep one shape by moving the other shape's paths out of the store (the top-level <id>.md files, or sessions/, entries/ and log.jsonl), " +
		"then run the same command again; the tool moves and deletes nothing"
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
	// The next action is in words: no command that moves or deletes anything.
	for _, cmd := range []string{"mv ", "rm ", "find ", "mkdir", "&&", "-exec"} {
		if strings.Contains(err.Error(), cmd) {
			t.Errorf("the refusal prints %q: it must name the next action in words only: %v", cmd, err)
		}
	}
}

// An over-long id of multi-byte characters is shown cut at a character, never
// inside one.
func TestLongIDIsShownCutAtARuneBoundary(t *testing.T) {
	t.Parallel()
	for pad := 0; pad < 4; pad++ {
		id := strings.Repeat("a", pad) + strings.Repeat("é", 100)
		err := badID("session", id)
		if !utf8.ValidString(err.Error()) {
			t.Fatalf("pad %d: the message holds a split character: %q", pad, err)
		}
	}
}

func TestIDRefusalsNameTheRuleBroken(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ id, want string }{
		{"x..y", `contains ".."`},
		{strings.Repeat("a", 129), "is 129 bytes long; the limit is 128"},
		{"a b", "contains whitespace"},
		{"a/b", "contains a slash or backslash"},
		{"a\x01b", "contains a control character"},
		{"..", "is a directory name"},
		{"", "is empty"},
	} {
		store := t.TempDir()
		for name, err := range map[string]error{
			"session": Open(store, tc.id, "", benchNow, PublishManual),
			"entry":   func() error { _, err := Append(store, "s", tc.id, "w", "", benchNow, PublishManual); return err }(),
		} {
			if tc.id == "" && name == "session" {
				continue
			}
			if err == nil || !strings.Contains(err.Error(), "it "+tc.want) || !strings.Contains(err.Error(), "bad "+name+" id") || strings.Contains(err.Error(), "no slashes, no whitespace") {
				t.Errorf("%s %q: %v", name, tc.id, err)
			}
		}
		if len(tc.id) > 128 {
			if _, err := Receipt(store, "s", tc.id); err == nil || strings.Contains(err.Error(), tc.id) {
				t.Errorf("receipt with a long id: %v", err)
			}
		}
	}
}

func TestEmptyAndAbsentStoresGetTheOwnShape(t *testing.T) {
	t.Parallel()
	for name, store := range map[string]string{
		"empty":  t.TempDir(),
		"absent": filepath.Join(t.TempDir(), "new", "cairns"),
	} {
		if err := Open(store, "s1", "src", benchNow, PublishManual); err != nil {
			t.Fatalf("%s: open: %v", name, err)
		}
		if got := listing(t, store); !reflect.DeepEqual(got, []string{"log.jsonl", "sessions/"}) {
			t.Fatalf("%s: a new store must get the tool's own shape, got %v", name, got)
		}
		if _, err := Append(store, "s1", "e1", "words", "", benchNow, PublishManual); err != nil {
			t.Fatalf("%s: append: %v", name, err)
		}
		if got := listing(t, store); !reflect.DeepEqual(got, []string{"entries/", "log.jsonl", "sessions/"}) {
			t.Fatalf("%s: listing after append %v", name, got)
		}
	}
}

func TestStoreShapeFromContents(t *testing.T) {
	t.Parallel()
	dir := func(files map[string]bool) string { // name -> isDir
		store := t.TempDir()
		for n, isDir := range files {
			if isDir {
				if err := os.Mkdir(filepath.Join(store, n), 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(store, n), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return store
	}
	for _, tc := range []struct {
		name  string
		files map[string]bool
		want  shape
	}{
		{"empty", nil, shapeOwn},
		{"one bench file", map[string]bool{"s.md": false}, shapeBench},
		{"bench with other files", map[string]bool{"s.md": false, "notes.txt": false, "sub": true}, shapeBench},
		{"only sessions", map[string]bool{"sessions": true}, shapeOwn},
		{"only log", map[string]bool{"log.jsonl": false}, shapeOwn},
		{"a directory named like a session", map[string]bool{"s.md": true}, shapeOwn},
		{"an invalid id is not a session", map[string]bool{"a b.md": false}, shapeOwn},
		{"both", map[string]bool{"s.md": false, "sessions": true}, shapeMixed},
	} {
		got, err := storeShape("open", dir(tc.files))
		if got != tc.want || (tc.want == shapeMixed) != (err != nil) {
			t.Errorf("%s: shape %v err %v, want %v", tc.name, got, err, tc.want)
		}
	}
	if got, err := storeShape("open", filepath.Join(t.TempDir(), "absent")); got != shapeOwn || err != nil {
		t.Errorf("absent: %v %v", got, err)
	}
}

// readmeNames are the spellings the README tests try. The unit tier tries one;
// the functional tier adds the other cases (readme_functional_test.go).
var readmeNames = []string{"README.md"}

// README.md, in any case, is documentation at the top of a store and never a
// session file, in either shape.
func TestReadmeIsNeverASessionFile(t *testing.T) {
	t.Parallel()
	for _, name := range readmeNames {
		// An own-shape store with a README beside its markers is own, not mixed.
		own := t.TempDir()
		if err := os.WriteFile(filepath.Join(own, name), []byte("# about this store\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := storeShape("open", own); got != shapeOwn || err != nil {
			t.Fatalf("%s alone: shape %v err %v, want own", name, got, err)
		}
		if err := Open(own, "s1", "", benchNow, PublishManual); err != nil {
			t.Fatalf("%s: open in an own-shape store: %v", name, err)
		}
		if got, err := storeShape("append", own); got != shapeOwn || err != nil {
			t.Fatalf("%s beside own-shape markers: shape %v err %v, want own", name, got, err)
		}
		if _, err := Append(own, "s1", "e1", "words", "", benchNow, PublishManual); err != nil {
			t.Fatalf("%s: append in an own-shape store: %v", name, err)
		}
		if _, total, err := Index(own, "", 0); err != nil || total != 1 {
			t.Fatalf("%s: index %d %v", name, total, err)
		}
		if got := Coverage(own).Sessions; got != 1 {
			t.Fatalf("%s: coverage counted %d sessions in an own-shape store", name, got)
		}

		// A bench store with a README is bench, and index does not list it.
		bench := t.TempDir()
		text := "# about\n\n## 2026-09-29T08:00:00Z — not-an-entry\n\nwords\n"
		if err := os.WriteFile(filepath.Join(bench, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(benchFile(bench, "s1"), []byte("# s1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := storeShape("open", bench); got != shapeBench || err != nil {
			t.Fatalf("%s beside a bench file: shape %v err %v, want bench", name, got, err)
		}
		if _, err := Append(bench, "s1", "e1", "words", "", benchNow, PublishManual); err != nil {
			t.Fatal(err)
		}
		rows, total, err := Index(bench, "", 0)
		if err != nil || total != 1 || len(rows) != 1 || rows[0].Session != "s1" {
			t.Fatalf("%s: index lists %v total %d err %v; the README is not a session", name, rows, total, err)
		}
		if got := Coverage(bench).Sessions; got != 1 {
			t.Fatalf("%s: coverage counted %d sessions, want 1", name, got)
		}

	}
}

// A store holding only a README.md holds no session: it is the tool's own
// shape, and the first open gives it the own layout beside the README.
func TestStoreHoldingOnlyReadmeIsAnEmptyStore(t *testing.T) {
	t.Parallel()
	for _, name := range readmeNames {
		store := t.TempDir()
		if err := os.WriteFile(filepath.Join(store, name), []byte("# about\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := storeShape("open", store); got != shapeOwn || err != nil {
			t.Fatalf("%s alone: shape %v %v, want own", name, got, err)
		}
		if rows, total, err := Index(store, "", 0); err != nil || total != 0 || len(rows) != 0 {
			t.Fatalf("%s alone: index %v %d %v", name, rows, total, err)
		}
		if got := Coverage(store).Sessions; got != 0 {
			t.Fatalf("%s alone: coverage %d", name, got)
		}
		if err := Open(store, "s1", "", benchNow, PublishManual); err != nil {
			t.Fatal(err)
		}
		want := []string{name, "log.jsonl", "sessions/"}
		sort.Strings(want)
		if got := listing(t, store); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s alone then open: %v", name, got)
		}
	}
}

func TestReadmeIsRefusedAsASessionIDByEveryVerb(t *testing.T) {
	t.Parallel()
	store, before := manySessionStore(t)
	for _, id := range []string{"README", "readme", "ReadMe"} {
		checks := map[string]error{
			"open":    Open(store, id, "", benchNow, PublishManual),
			"append":  func() error { _, err := Append(store, id, "e", "w", "", benchNow, PublishManual); return err }(),
			"index":   func() error { _, _, err := Index(store, id, 0); return err }(),
			"receipt": func() error { _, err := Receipt(store, id, "e"); return err }(),
		}
		for verb, err := range checks {
			if err == nil || !strings.Contains(err.Error(), "is reserved") || !strings.Contains(err.Error(), "README.md") {
				t.Errorf("%s --session %s: want a refusal naming the reserved name, got %v", verb, id, err)
			}
		}
	}
	if got := listing(t, store); !reflect.DeepEqual(got, before) {
		t.Fatalf("a refused verb changed the store: %v", got)
	}
	// An entry may still be called README.
	if err := Open(store, "NEW", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(store, "NEW", "README", "words", "", benchNow, PublishManual); err != nil {
		t.Fatalf("entry id README: %v", err)
	}
}

func TestReadmeBesideOwnMarkersAndAnotherSessionFileStaysMixed(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	for _, n := range []string{"README.md", "hand.md"} {
		if err := os.WriteFile(filepath.Join(store, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(store, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Open(store, "s", "", benchNow, PublishManual)
	var me *MixedShapeError
	if !errors.As(err, &me) || strings.Contains(err.Error(), "README") {
		t.Fatalf("want a mixed refusal naming hand.md only, got %v", err)
	}
}

// Many opens of one new session at once: exactly one header is written, every
// caller succeeds, and no temporary file is left behind.
func TestConcurrentBenchOpensWriteOneHeader(t *testing.T) {
	t.Parallel()
	store, before := manySessionStore(t)
	const n = 32
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- Open(store, "NEW", "src-"+strconv.Itoa(i), benchNow, PublishManual)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a concurrent open failed: %v", err)
		}
	}
	want := append(append([]string(nil), before...), "NEW.md")
	sort.Strings(want)
	if got := listing(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("listing %v want %v (no temporary file may remain)", got, want)
	}
	raw, err := os.ReadFile(benchFile(store, "NEW"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "# Cairn ") != 1 || strings.Count(string(raw), "Session NEW opened") != 1 || strings.Count(string(raw), "Source: ") != 1 {
		t.Fatalf("not exactly one header:\n%s", raw)
	}
}

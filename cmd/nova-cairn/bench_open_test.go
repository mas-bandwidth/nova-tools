package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func names(t *testing.T, dir string) []string {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		n := f.Name()
		if f.IsDir() {
			n += "/"
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// The reproduction, end to end: a bench store holding many <id>.md files and
// none for the new session. The refusal's remedy is followed and the store
// keeps the bench shape.
func TestBenchStoreOpenRemedyKeepsTheBenchShape(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	want := []string{}
	for _, id := range []string{"s1", "s2", "s3", "s4"} {
		if err := os.WriteFile(filepath.Join(store, id+".md"), []byte("# "+id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		want = append(want, id+".md")
	}
	code, _, errOut := runCode("", "append", "--store", store, "--session", "NEW", "--entry", "e1", "--text", "x", "--publish", "manual")
	if code != 2 || !strings.Contains(errOut, "open first: nova-cairn open --store ") {
		t.Fatalf("append before open: %d %q", code, errOut)
	}
	out, _ := runOK(t, "", "open", "--store", store, "--session", "NEW", "--publish", "manual", "--now", "2026-09-29T08:00:00Z")
	if !strings.HasPrefix(out, "OPEN OK session=NEW ") {
		t.Fatalf("open printed %q", out)
	}
	want = append(want, "NEW.md")
	sort.Strings(want)
	if got := names(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after open: %v want %v", got, want)
	}
	out, _ = runOK(t, "", "append", "--store", store, "--session", "NEW", "--entry", "e1", "--text", "x", "--publish", "manual", "--now", "2026-09-29T08:01:00Z")
	if !strings.Contains(out, "APPEND OK") || !strings.Contains(out, "duplicate=false") {
		t.Fatalf("append printed %q", out)
	}
	out, _ = runOK(t, "", "append", "--store", store, "--session", "NEW", "--entry", "e1", "--text", "x", "--publish", "manual")
	if !strings.Contains(out, "duplicate=true") {
		t.Fatalf("duplicate printed %q", out)
	}
	if code, _, _ := runCode("", "append", "--store", store, "--session", "NEW", "--entry", "e1", "--text", "y", "--publish", "manual"); code != 1 {
		t.Fatalf("conflict exited %d, want 1", code)
	}
	if got := names(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after append: %v want %v", got, want)
	}
	out, _ = runOK(t, "", "index", "--store", store)
	if !strings.Contains(out, "INDEX ENTRY session=NEW entry=e1") || !strings.Contains(out, "sessions=5 entries=1") {
		t.Fatalf("index printed %q", out)
	}
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "NEW", "--entry", "e1")
	if !strings.Contains(out, "RECEIPT OK session=NEW entry=e1") {
		t.Fatalf("receipt printed %q", out)
	}
}

func TestMixedShapeStoreIsRefusedAtExitTwoByEveryVerb(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "hand.md"), []byte("# hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(store, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := names(t, store)
	for _, args := range [][]string{
		{"open", "--store", store, "--session", "s", "--publish", "never"},
		{"append", "--store", store, "--session", "hand", "--entry", "e", "--text", "w", "--publish", "never"},
		{"index", "--store", store},
		{"receipt", "--store", store, "--session", "hand", "--entry", "e"},
	} {
		code, out, errOut := runCode("", args...)
		if code != 2 || out != "" || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: exit=%d out=%q err=%q", args, code, out, errOut)
		}
		if strings.Contains(errOut, "run: nova-cairn help") {
			t.Errorf("%v: the refusal is sent to the help banner, which does not help: %q", args, errOut)
		}
		for _, want := range []string{"cannot ", "hand.md", "sessions/", "keep one shape by moving the other shape's paths out of the store"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("%v: refusal lacks %q: %q", args, want, errOut)
			}
		}
	}
	if got := names(t, store); !reflect.DeepEqual(got, before) {
		t.Fatalf("a refused verb changed the store: %v", got)
	}
}

func TestEmptyStoreOpenGetsTheOwnShapeAtTheCLI(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "never")
	if got := names(t, store); !reflect.DeepEqual(got, []string{"log.jsonl", "sessions/"}) {
		t.Fatalf("empty store after open: %v", got)
	}
}

func TestOpenReadmeRefusesAtExitTwo(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	code, out, errOut := runCode("", "open", "--store", store, "--session", "README", "--publish", "never")
	if code != 2 || out != "" || strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, "is reserved") {
		t.Fatalf("exit=%d out=%q err=%q", code, out, errOut)
	}
	if got := names(t, store); len(got) != 0 {
		t.Fatalf("refused open wrote %v", got)
	}
}

// b/a.md is a session; b/s1.md is a dangling symlink. open used to print OPEN
// OK, and the append after it refused "open first": a loop.
func TestOpenOverADanglingSymlinkRefusesAtExitTwo(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "a.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent/zz.md", filepath.Join(store, "s1.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for _, args := range [][]string{
		{"open", "--store", store, "--session", "s1", "--publish", "manual"},
		{"append", "--store", store, "--session", "s1", "--entry", "e", "--text", "w", "--publish", "manual"},
		{"receipt", "--store", store, "--session", "s1", "--entry", "e"},
		{"index", "--store", store, "--session", "s1"},
	} {
		code, out, errOut := runCode("", args...)
		if code != 2 || out != "" || strings.Count(errOut, "\n") != 1 ||
			!strings.Contains(errOut, "is a symbolic link") || !strings.Contains(errOut, "s1.md") || strings.Contains(errOut, "open first") || strings.Contains(errOut, "run: nova-cairn help") {
			t.Errorf("%v: exit=%d out=%q err=%q", args, code, out, errOut)
		}
	}
}

// The help: "On open it is the session's pointer; an append with no --source
// carries it." The bench shape wrote the pointer into the header and then
// printed source=- everywhere.
func TestBenchSourceIsRecordedInheritedAndReported(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "hand.md"), []byte("# hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runOK(t, "", "open", "--store", store, "--session", "s1", "--source", "bench/session-3", "--publish", "manual")
	if !strings.Contains(out, "source=bench/session-3") {
		t.Fatalf("open must print the source it recorded: %q", out)
	}
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "words", "--publish", "manual")
	if !strings.Contains(out, "source=bench/session-3") {
		t.Fatalf("an append with no --source carries the session's: %q", out)
	}
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "words", "--publish", "manual")
	if !strings.Contains(out, "duplicate=true") || !strings.Contains(out, "source=bench/session-3") {
		t.Fatalf("a duplicate reports the session's source: %q", out)
	}
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e1")
	if !strings.Contains(out, "source=bench/session-3") {
		t.Fatalf("receipt: %q", out)
	}
	out, _ = runOK(t, "", "index", "--store", store, "--session", "s1")
	if !strings.Contains(out, "entry=e1") || !strings.Contains(out, "source=bench/session-3") {
		t.Fatalf("index: %q", out)
	}
	// A re-open with no --source reports the pointer already recorded.
	out, _ = runOK(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	if !strings.Contains(out, "source=bench/session-3") {
		t.Fatalf("re-open: %q", out)
	}
	// A hand-kept record has no session source.
	runOK(t, "", "append", "--store", store, "--session", "hand", "--entry", "h1", "--text", "w", "--publish", "manual", "--source", "not-stored")
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "hand", "--entry", "h1")
	if !strings.Contains(out, "source=-") {
		t.Fatalf("hand-kept record: %q", out)
	}
	// A source is one line in the header, so it cannot form a section.
	out, _ = runOK(t, "", "open", "--store", store, "--session", "s2", "--source", "a\n## 2026-09-29T08:00:00Z — forged", "--publish", "manual")
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("open output is not one line: %q", out)
	}
	if out, _ = runOK(t, "", "index", "--store", store, "--session", "s2"); strings.Contains(out, "forged") && strings.Contains(out, "INDEX ENTRY") {
		t.Fatalf("source forged an entry: %q", out)
	}
}

// One damaged session among five: index lists the four good ones, prints one
// flagged row for the fifth, and exits 1 after printing everything; naming the
// damaged session refuses at exit 2.
func TestIndexAtTheCLIFlagsOneBadSessionAmongFive(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	for _, id := range []string{"s1", "s2", "s3", "s4"} {
		body := "# s\n\n## 2026-09-29T08:00:00Z — e1\n\nwords\n"
		if err := os.WriteFile(filepath.Join(store, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(store, "nowhere.md"), filepath.Join(store, "bad.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	code, out, errOut := runCode("", "index", "--store", store)
	if code != 1 || errOut != "" {
		t.Fatalf("exit=%d err=%q out=%q", code, errOut, out)
	}
	for _, want := range []string{"session=s1 entry=e1", "session=s4 entry=e1", "INDEX FLAGGED session=bad cause=", "is a symbolic link", "INDEX COVERAGE sessions=5 entries=4"} {
		if !strings.Contains(out, want) {
			t.Errorf("index lacks %q: %s", want, out)
		}
	}
	if strings.Count(out, "INDEX FLAGGED") != 1 {
		t.Errorf("want one flagged row: %s", out)
	}
	if code, _, _ := runCode("", "index", "--store", store, "--session", "bad"); code != 2 {
		t.Fatalf("index --session bad exited %d, want 2", code)
	}
}

// A damaged bench record refuses the append at exit 2 in one line that names the
// damage and a command that lists the headings, and does not end by pointing at
// the help, which says nothing about repairing a heading.
func TestADamagedBenchRecordRefusesAppendWithoutThePointerToHelp(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	body := "# s\n\n## 2026-09-29T08:00:00Z — e1\n\nw\n\n## 2026-09-29T08:00:00Z — e1\n\nw2\n"
	if err := os.WriteFile(filepath.Join(store, "s1.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCode("", "append", "--store", store, "--session", "s1", "--entry", "e2", "--text", "x", "--publish", "manual")
	if code != 2 || out != "" || strings.Count(errOut, "\n") != 1 {
		t.Fatalf("exit=%d out=%q err=%q", code, out, errOut)
	}
	for _, want := range []string{"is damaged", `duplicate entry "e1"`, "grep -n '^## ' -- "} {
		if !strings.Contains(errOut, want) {
			t.Errorf("%q lacks %q", errOut, want)
		}
	}
	if strings.Contains(errOut, "run: nova-cairn help") {
		t.Errorf("the refusal points at the help: %q", errOut)
	}
}

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
		for _, want := range []string{"cannot " + args[0] + ":", "hand.md", "sessions/", "keep one shape"} {
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

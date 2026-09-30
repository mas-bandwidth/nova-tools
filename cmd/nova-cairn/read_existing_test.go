package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCommandsRefuseMissingStoreAndSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, tc := range []struct{ store, session, want string }{
		{filepath.Join(root, "missing"), "s", "store"},
		{root, "missing", "session"},
		{root, "../escape", "session"},
	} {
		for _, verb := range []string{"index", "receipt"} {
			args := []string{verb, "--store", tc.store, "--session", tc.session}
			if verb == "receipt" {
				args = append(args, "--entry", "e")
			}
			code, out, errOut := runCode("", args...)
			if code != 2 || out != "" || !strings.Contains(errOut, tc.want) {
				t.Errorf("%q exit=%d out=%q err=%q", args, code, out, errOut)
			}
		}
	}
	code, out, errOut := runCode("", "index", "--store", filepath.Join(root, "missing"))
	if code != 2 || out != "" || !strings.Contains(errOut, "store") {
		t.Errorf("missing unfiltered store: %d %q %q", code, out, errOut)
	}
	out, _ = runOK(t, "", "index", "--store", root)
	if !strings.Contains(out, "sessions=0 entries=0") {
		t.Fatalf("empty existing store: %q", out)
	}
	runOK(t, "", "open", "--store", root, "--session", "empty", "--publish", "never")
	out, _ = runOK(t, "", "index", "--store", root, "--session", "empty")
	if !strings.Contains(out, "entries=0") {
		t.Fatalf("empty existing session: %q", out)
	}
}

func TestIndexAndReceiptReadFlatRecordsWithoutChangingThem(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	path := filepath.Join(store, "flat.md")
	if err := os.WriteFile(path, []byte("# A session\n\nUnstructured opening prose.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"early", "late"} {
		stamp := "2026-09-28T01:02:03Z"
		if id == "late" {
			stamp = "2026-09-28T02:02:03Z"
		}
		runOK(t, "", "append", "--store", store, "--session", "flat", "--entry", id, "--text", "a note", "--now", stamp, "--publish", "manual", "--source", "not-stored")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := runOK(t, "", "index", "--store", store, "--max", "1")
	for _, want := range []string{"INDEX ENTRY session=flat entry=early stamp=2026-09-28T01:02:03Z bytes=6 source=-", "INDEX MORE kind=entry shown=1 total=2", "INDEX OK sessions=1 entries=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("index lacks %q: %s", want, out)
		}
	}
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "flat", "--entry", "late")
	for _, want := range []string{"stamp=2026-09-28T02:02:03Z", "bytes=6 source=-", "publish=unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("receipt lacks %q: %s", want, out)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("read changed the session")
	}
	files, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("read added sidecars: %v", files)
	}
	code, _, errOut := runCode("", "receipt", "--store", store, "--session", "flat", "--entry", "absent")
	if code != 2 || !strings.Contains(errOut, "entry") {
		t.Fatalf("absent entry: %d %q", code, errOut)
	}
}

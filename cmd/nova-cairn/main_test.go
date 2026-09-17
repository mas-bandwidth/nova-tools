// CLI tests for nova-tools #248: the four verbs work end to end, and the
// lifecycle verbs the issue rules out stay refused.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runOK(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var in *strings.Reader
	if stdin == "" {
		in = strings.NewReader("")
	} else {
		in = strings.NewReader(stdin)
	}
	if code := run(args, in, &stdout, &stderr); code != 0 {
		t.Fatalf("run %v exited %d: stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func runCode(stdin string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestOpenAppendIndexReceiptRoundTrip(t *testing.T) {
	store := t.TempDir()
	out, _ := runOK(t, "", "open", "--store", store, "--session", "s1", "--source", "bench/session-3", "--publish", "manual")
	if !strings.Contains(out, "OPEN OK") || !strings.Contains(out, "session=s1") {
		t.Fatalf("open printed %q", out)
	}
	prose := "the friend's chosen words — café, \"as above\" nowhere, byte-exact"
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", prose, "--source", "bench/session-3#L9", "--publish", "manual")
	if !strings.Contains(out, "APPEND OK") || !strings.Contains(out, "persisted=true published=false") {
		t.Fatalf("append printed %q", out)
	}
	// The duplicate request succeeds without a duplicate entry.
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", prose, "--publish", "manual")
	if !strings.Contains(out, "duplicate=true") {
		t.Fatalf("retry printed %q, want duplicate=true", out)
	}
	// Same id, different prose: exit 1, never an overwrite.
	if code, _, _ := runCode("", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", "other words", "--publish", "manual"); code != 1 {
		t.Fatalf("conflicting append exited %d, want 1", code)
	}
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e1")
	if !strings.Contains(out, "RECEIPT OK") || !strings.Contains(out, "persisted=true published=false") {
		t.Fatalf("receipt printed %q", out)
	}
	out, _ = runOK(t, "", "index", "--store", store)
	if !strings.Contains(out, "INDEX ENTRY session=s1 entry=e1") {
		t.Fatalf("index printed %q", out)
	}
	if !strings.Contains(out, "INDEX COVERAGE sessions=1 entries=1") {
		t.Fatalf("coverage printed %q", out)
	}
	raw, err := os.ReadFile(filepath.Join(store, "entries", "s1", "e1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Text != prose {
		t.Fatalf("stored entry holds %q, want the exact prose %q", stored.Text, prose)
	}
}

func TestOfflineAppendSucceedsWithPublicationPending(t *testing.T) {
	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "deferred")
	out, _ := runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "e",
		"--text", "offline note", "--publish", "deferred")
	if !strings.Contains(out, "persisted=true published=false") {
		t.Fatalf("offline append printed %q", out)
	}
}

func TestAppendViaFileAndStdinKeepsExactBytes(t *testing.T) {
	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "never")
	prose := "line one\nline two  with trailing spaces   \n\ttabbed\n"
	f := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(f, []byte(prose), 0o644); err != nil {
		t.Fatal(err)
	}
	runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "from-file", "--file", f, "--publish", "never")
	runOK(t, prose, "append", "--store", store, "--session", "s", "--entry", "from-stdin", "--file", "-", "--publish", "never")
	for _, id := range []string{"from-file", "from-stdin"} {
		out, _ := runOK(t, "", "receipt", "--store", store, "--session", "s", "--entry", id)
		if !strings.Contains(out, "RECEIPT OK") {
			t.Fatalf("receipt %s printed %q", id, out)
		}
	}
	raw, err := os.ReadFile(filepath.Join(store, "entries", "s", "from-stdin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Text != prose {
		t.Fatalf("stdin bytes not preserved: got %q want %q", stored.Text, prose)
	}
}

func TestInterruptedAppendRecoversAtCLI(t *testing.T) {
	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "manual")
	runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "other",
		"--text", "other writer's note", "--publish", "manual")
	edir := filepath.Join(store, "entries", "s")
	if err := os.MkdirAll(edir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(edir, "mine.json.tmp"), []byte("{partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "mine",
		"--text", "my note after the crash", "--publish", "manual")
	if !strings.Contains(out, "APPEND OK") {
		t.Fatalf("recovery printed %q", out)
	}
	out, _ = runOK(t, "", "index", "--store", store)
	if !strings.Contains(out, "entries=2") {
		t.Fatalf("index after recovery printed %q", out)
	}
	if !strings.Contains(out, "INDEX ENTRY session=s entry=other") || !strings.Contains(out, "INDEX ENTRY session=s entry=mine") {
		t.Fatalf("other writer lost or partial indexed: %q", out)
	}
}

func TestExistingDirtyWorkIsUntouched(t *testing.T) {
	store := t.TempDir()
	dirty := filepath.Join(store, "my-unfinished-work.md")
	before := []byte("half-written thought, do not touch\n")
	if err := os.WriteFile(dirty, before, 0o644); err != nil {
		t.Fatal(err)
	}
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "never")
	runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "e",
		"--text", "a checkpoint alongside dirty work", "--publish", "never")
	runOK(t, "", "index", "--store", store)
	after, err := os.ReadFile(dirty)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("dirty work changed to %q", after)
	}
	if _, err := os.Stat(filepath.Join(store, ".git")); !os.IsNotExist(err) {
		t.Fatalf("tool must not init or touch version control in the store")
	}
}

func TestConcurrentRecordsAndAlternateHeaders(t *testing.T) {
	store := t.TempDir()
	for _, s := range []string{"alpha", "beta"} {
		runOK(t, "", "open", "--store", store, "--session", s, "--publish", "never")
		runOK(t, "", "append", "--store", store, "--session", s, "--entry", "e",
			"--text", "note in "+s, "--publish", "never")
	}
	sessFile := filepath.Join(store, "sessions", "alpha.md")
	raw, err := os.ReadFile(sessFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	lines[0] = "# Our team files records under its own headings"
	if err := os.WriteFile(sessFile, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runOK(t, "", "index", "--store", store)
	if !strings.Contains(out, "INDEX COVERAGE sessions=2 entries=2") {
		t.Fatalf("index printed %q", out)
	}
	out, _ = runOK(t, "", "index", "--store", store, "--session", "alpha")
	if !strings.Contains(out, "INDEX ENTRY session=alpha entry=e") {
		t.Fatalf("per-session index printed %q", out)
	}
}

func TestLifecycleVerbsStayRefused(t *testing.T) {
	store := t.TempDir()
	for _, verb := range []string{"seal", "consume", "delete", "grade", "consolidate", "wake", "rollup", "retention"} {
		if code, _, _ := runCode("", verb, "--store", store); code != 2 {
			t.Fatalf("%s exited %d, want 2 (unknown subcommand)", verb, code)
		}
	}
}

func TestMissingFlagsAreRefusedNeverGuessed(t *testing.T) {
	if code, _, _ := runCode("", "open", "--session", "s"); code != 2 {
		t.Fatalf("open without --store exited %d, want 2", code)
	}
	if code, _, _ := runCode("", "append", "--store", "x", "--session", "s", "--entry", "e",
		"--publish", "never"); code != 2 {
		t.Fatalf("append without words exited %d, want 2", code)
	}
	if code, _, _ := runCode("", "append", "--store", "x", "--session", "s"); code != 2 {
		t.Fatalf("append without --entry/--publish exited %d, want 2", code)
	}
}

func TestBadClockIsRefused(t *testing.T) {
	store := t.TempDir()
	if code, _, _ := runCode("", "open", "--store", store, "--session", "s",
		"--publish", "never", "--now", "tomorrow-ish"); code != 2 {
		t.Fatalf("open with bad --now exited %d, want 2", code)
	}
}

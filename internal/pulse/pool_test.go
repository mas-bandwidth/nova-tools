package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ghFixture puts a fake `gh` on PATH that serves issue list JSON and records every argv it
// was called with, and returns the path of that record. The record is the proof: a real gh
// writes nothing to it, so a test that asserts the argv cannot pass on a bench that merely
// happens to be logged in.
func ghFixture(t *testing.T, dir, jsonBody string) string {
	t.Helper()
	specs := fakePATH(t)
	log := filepath.Join(dir, "gh-argv.log")
	fakeTool(t, specs, "gh", fakeSpec{Log: log, Default: fakeRule{StdoutFile: jsonBody}})
	return log
}

// ghCalls is the fake gh's argv record, one line per invocation.
func ghCalls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return nonempty(string(raw))
}

func TestPoolReadsIssueLabel(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeTestFile(t, dir, "issues.json", `[
  {"number": 1, "title": "one", "labels": [{"name": "card"}], "body": ""},
  {"number": 2, "title": "two", "labels": [{"name": "card"}], "body": ""},
  {"number": 3, "title": "three", "labels": [], "body": "tool: x\ncommand: y\nverbatim output: z\nexpected: a\nsmallest fix: b"},
  {"number": 4, "title": "four", "labels": [], "body": "not a card"}
]`)
	ghLog := ghFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeTestFile(t, dir, "sources.tsv", "issues\towner/repo\tfix\n")

	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("Pool exit = %d, stderr=%s", code, errb.String())
	}
	// The fake answered, not the real gh: the argv it recorded is the only thing that can
	// have produced the rows below.
	calls := ghCalls(t, ghLog)
	if len(calls) != 1 || !strings.Contains(calls[0], "gh issue list --repo owner/repo") {
		t.Fatalf("the fake gh recorded %v; want one `gh issue list --repo owner/repo`", calls)
	}
	if !strings.Contains(out.String(), "candidates=3") || !strings.Contains(out.String(), "issues=3") {
		t.Fatalf("POOL OK line wrong: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 pool rows, got %d: %q", len(lines), string(raw))
	}
	for i, wantID := range []string{"1", "2", "3"} {
		if got := strings.Split(lines[i], "\t")[1]; got != wantID {
			t.Fatalf("row %d id = %q, want %q", i, got, wantID)
		}
	}
}

func TestPoolRefusesUnreadableSource(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// A gh that exits 1: the issues source is unreadable.
	specs := fakePATH(t)
	ghLog := filepath.Join(dir, "gh-argv.log")
	fakeTool(t, specs, "gh", fakeSpec{Log: ghLog, Default: fakeRule{Exit: 1}})

	sources := writeTestFile(t, dir, "sources.tsv", "issues\towner/repo\tfix\n")
	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "POOL REFUSED source=issues:owner/repo") {
		t.Fatalf("refusal line wrong: %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "pool.tsv")); !os.IsNotExist(err) {
		t.Fatalf("pool.tsv was written despite the refusal")
	}
	if calls := ghCalls(t, ghLog); len(calls) != 1 {
		t.Fatalf("the fake gh recorded %v; the refusal must come from the fake, not a real gh", calls)
	}
}

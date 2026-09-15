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

// ghFixture writes a fake `gh` on PATH that serves issue list JSON and records argv.
func ghFixture(t *testing.T, dir, jsonBody string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// The fixture only needs to answer `gh issue list`; the JSON is a file so paths with
	// spaces and quotes survive the shell.
	script := "#!/bin/sh\ncat " + shellQuote(jsonBody)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func TestPoolReadsIssueLabel(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeTestFile(t, dir, "issues.json", `[
  {"number": 1, "title": "one", "labels": [{"name": "card"}], "body": ""},
  {"number": 2, "title": "two", "labels": [{"name": "card"}], "body": ""},
  {"number": 3, "title": "three", "labels": [], "body": "tool: x\ncommand: y\nverbatim output: z\nexpected: a\nsmallest fix: b"},
  {"number": 4, "title": "four", "labels": [], "body": "not a card"}
]`)
	ghFixture(t, dir, jsonBody)

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
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

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
}

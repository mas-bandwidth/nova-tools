package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMainFile(t *testing.T, dir, name, content string) string {
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

func mainGhFixture(t *testing.T, dir, jsonBody string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + `"` + strings.ReplaceAll(jsonBody, `"`, `\"`) + `"`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPoolMaxFlagBoundsCandidates(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeMainFile(t, dir, "issues.json", `[
  {"number": 1, "title": "one", "labels": [{"name": "card"}], "body": ""},
  {"number": 2, "title": "two", "labels": [{"name": "card"}], "body": ""},
  {"number": 3, "title": "three", "labels": [{"name": "card"}], "body": ""}
]`)
	mainGhFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeMainFile(t, dir, "sources.tsv", "issues\towner/repo\tfix\n")

	var out, errb bytes.Buffer
	code := run([]string{"pool", "--sources", sources, "--root", root, "--max", "1"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("pool exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "candidates=1") || !strings.Contains(out.String(), "issues=1") {
		t.Fatalf("POOL OK line bounds not honored: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 pool row, got %d: %q", len(lines), string(raw))
	}
}

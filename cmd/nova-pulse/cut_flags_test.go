package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCutDependsOnFlag(t *testing.T) {
	td := t.TempDir()
	tmplDir := filepath.Join(td, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	benches := "model\topencode/deepseek-v4-flash\topencode/deepseek-v4-pro\ncost\tflat 1.0\tflat 1.5\ncapability\tread|text|replay\tcode\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "benches.tsv"), []byte(benches), 0o644); err != nil {
		t.Fatal(err)
	}
	template := `RESULT <label> sha=<sha12>
KIND: fix
PATHS: internal/pulse/cut.go
TEST: ./internal/pulse/ TestCut
STEP 1. mkdir -p scratch && git clone -q https://example.com/<source>.git . && git checkout -b <branch>
red line
green line
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`
	if err := os.WriteFile(filepath.Join(tmplDir, "fix.md"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	pool := filepath.Join(td, "pool.tsv")
	if err := os.WriteFile(pool, []byte("mas-bandwidth/nova-tools\t1\tfix\tTitle\tfix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(td, "out")
	root := filepath.Join(td, "root")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"cut",
		"--pool", pool,
		"--templates", tmplDir,
		"--out", out,
		"--root", root,
		"--depends-on", "tools-01, tools-04",
	}, &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("run cut = %d, stderr=%s", code, stderr.String())
	}
	cardBytes, err := os.ReadFile(filepath.Join(out, "1.md"))
	if err != nil {
		t.Fatal(err)
	}
	card := string(cardBytes)
	want := "PATHS: internal/pulse/cut.go\nDEPENDS-ON: tools-01, tools-04\nTEST: ./internal/pulse/ TestCut"
	if !strings.Contains(card, want) {
		t.Errorf("card does not contain expected header:\ngot:\n%s\nwant substring:\n%s", card, want)
	}
}

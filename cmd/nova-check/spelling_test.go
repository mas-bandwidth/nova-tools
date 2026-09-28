package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSpelling(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"spelling"}, args...), &outBuf, &errBuf)
	return code, outBuf.String(), errBuf.String()
}

func TestSpellingCLIRefusesNoFlags(t *testing.T) {
	t.Parallel()
	code, _, stderr := runSpelling(t)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "give at least one of --dir, --file, or --path; refusing to guess") {
		t.Errorf("stderr does not contain refusal: %q", stderr)
	}
}

func TestSpellingCLIRefusesNegativeFailMax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, _, stderr := runSpelling(t, "--dir", dir, "--fail-max", "-1")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--fail-max must be a line ceiling of zero or more") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestSpellingCLICleanPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "clean.md")
	if err := os.WriteFile(f, []byte("A clean sentence with no spelling errors.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--dir", dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPELLING OK") || !strings.Contains(stdout, "misspellings=0") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSpellingCLIFindingsReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "typo.md")
	content := "We recieve the package.\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", f)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "SPELLING FAIL") || !strings.Contains(stderr, "recieve -> receive") {
		t.Errorf("stderr = %q", stderr)
	}
	// Verify file was NOT modified.
	cur, _ := os.ReadFile(f)
	if string(cur) != content {
		t.Errorf("file modified in read-only mode: %q", string(cur))
	}
}

func TestSpellingCLICodeFencesAndSpansIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "code.md")
	content := "Prose with `recieve` identifier.\n```go\nfunc recieve() {}\n```\nAll clean.\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", f)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPELLING OK") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSpellingCLIIgnoreFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(f, []byte("The colour of the code is optimise.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", f, "--ignore", "colour,optimise")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPELLING OK") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSpellingCLIIgnoreFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(f, []byte("The colour of the code.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignoreFile := filepath.Join(dir, "ignore.txt")
	if err := os.WriteFile(ignoreFile, []byte("# Words to ignore\ncolour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", f, "--ignore", "@"+ignoreFile)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPELLING OK") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSpellingCLIWriteMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	orig := "We recieve this.\n"
	if err := os.WriteFile(f, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", f, "--write")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPELLING FIXED") || !strings.Contains(stdout, "written=1") {
		t.Errorf("stdout = %q", stdout)
	}

	updated, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "We receive this.\n" {
		t.Errorf("updated content = %q, want %q", string(updated), "We receive this.\n")
	}
}

func TestSpellingCLIPathPatternAndExclude(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d1 := filepath.Join(dir, "docs")
	d2 := filepath.Join(dir, "vendor")
	_ = os.MkdirAll(d1, 0o755)
	_ = os.MkdirAll(d2, 0o755)

	_ = os.WriteFile(filepath.Join(d1, "a.md"), []byte("We recieve this.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d2, "b.md"), []byte("We recieve that.\n"), 0o644)

	code, _, stderr := runSpelling(t, "--dir", dir, "--exclude", "vendor")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "docs/a.md") {
		t.Errorf("expected docs/a.md in stderr, got: %q", stderr)
	}
	if strings.Contains(stderr, "vendor/b.md") {
		t.Errorf("vendor/b.md should have been excluded, but found in: %q", stderr)
	}
}

func TestSpellingCLIFailMax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "many.md")
	content := "recieve\nseperate\noccured\ncolour\noptimise\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runSpelling(t, "--file", f, "--fail-max", "2")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "SPELLING MORE kind=misspelling") {
		t.Errorf("expected MORE line in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "shown=2") {
		t.Errorf("expected shown=2 in summary line, got: %q", stderr)
	}
}

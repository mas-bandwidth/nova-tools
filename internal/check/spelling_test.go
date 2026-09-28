package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/check"
)

func TestSpellingSeededMisspellings(t *testing.T) {
	t.Parallel()
	text := "I recieve mail.\nWe seperate the parts.\nIt occured to me.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(findings), findings)
	}

	expected := []struct {
		line int
		orig string
		repl string
	}{
		{1, "recieve", "receive"},
		{2, "seperate", "separate"},
		{3, "occured", "occurred"},
	}

	for i, exp := range expected {
		f := findings[i]
		if f.Line != exp.line {
			t.Errorf("[%d] line = %d, want %d", i, f.Line, exp.line)
		}
		if f.Original != exp.orig {
			t.Errorf("[%d] orig = %q, want %q", i, f.Original, exp.orig)
		}
		if f.Replacement != exp.repl {
			t.Errorf("[%d] repl = %q, want %q", i, f.Replacement, exp.repl)
		}
	}
}

func TestSpellingBritishVariantsToUS(t *testing.T) {
	t.Parallel()
	text := "The colour of the code.\nWe optimise the loop.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	if findings[0].Original != "colour" || findings[0].Replacement != "color" {
		t.Errorf("finding 0 mismatch: %+v", findings[0])
	}
	if findings[1].Original != "optimise" || findings[1].Replacement != "optimize" {
		t.Errorf("finding 1 mismatch: %+v", findings[1])
	}
}

func TestSpellingAllowlist(t *testing.T) {
	t.Parallel()
	text := "The colour of the cairn.\n"
	opts := check.SpellingOptions{Ignore: []string{"colour"}}
	findings, _, err := check.CheckSpellingText("test.md", text, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected colour to be ignored, got: %+v", findings)
	}
}

func TestSpellingAllowlistCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"colour", "Colour", "COLOUR"} {
		text := "The " + word + " of the cairn.\n"
		opts := check.SpellingOptions{Ignore: []string{"Colour"}}
		findings, _, err := check.CheckSpellingText("test.md", text, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("expected %q to be ignored by allowlist entry, got: %+v", word, findings)
		}
	}
}

func TestSpellingAllowlistFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	listFile := filepath.Join(dir, "allow.txt")
	content := "# Words we allow\ncolour\n  optimise  # comment\n\n"
	if err := os.WriteFile(listFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing allowlist file: %v", err)
	}

	opts := check.SpellingOptions{Ignore: []string{"@" + listFile}}
	text := "We optimise the colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected findings to be empty with allowlist file, got: %+v", findings)
	}
}

func TestSpellingFencedCodeBlockBlanking(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n```go\nrecieve := seperate(occured)\n```\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Line != 5 || findings[0].Original != "colour" {
		t.Errorf("finding line or word mismatch: %+v", findings[0])
	}
}

func TestSpellingTildeFencedCodeBlock(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n~~~python\nrecieve = 1\n~~~\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Line != 5 || findings[0].Original != "colour" {
		t.Errorf("finding line or word mismatch: %+v", findings[0])
	}
}

func TestSpellingInlineCodeSpanBlanking(t *testing.T) {
	t.Parallel()
	text := "The `recieve` identifier is code, but occured is prose.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Original != "occured" {
		t.Errorf("expected occured, got %+v", findings[0])
	}
}

func TestSpellingUnmatchedBacktickLiteral(t *testing.T) {
	t.Parallel()
	text := "A stray ` and then recieve.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Original != "recieve" {
		t.Errorf("expected recieve, got %+v", findings[0])
	}
}

func TestSpellingUnclosedFenceBlanksToEnd(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n```go\nrecieve := 1\nseperate := 2\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected unclosed fence to blank to end, got: %+v", findings)
	}
}

func TestStripCodePreservesLengthAndLines(t *testing.T) {
	t.Parallel()
	cases := []string{
		"plain text\n",
		"no trailing newline",
		"inline `code` span and ``double`tick`` span\n",
		"```go\nvar x = 1\n```\ntail line\n",
		"multibyte unicode — dash and ``código``\n",
		"",
	}
	for _, c := range cases {
		stripped := check.StripCode(c)
		if len(stripped) != len(c) {
			t.Errorf("len mismatch: orig %d, stripped %d for %q", len(c), len(stripped), c)
		}
		if strings.Count(stripped, "\n") != strings.Count(c, "\n") {
			t.Errorf("newline count mismatch for %q", c)
		}
	}
}

func TestSpellingWriteMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	content := "Start line.\nWe recieve data and `seperate` it, but colour is real.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	opts := check.SpellingOptions{Write: true}
	findings, err := check.CheckSpellingFile(path, opts)
	if err != nil {
		t.Fatalf("CheckSpellingFile: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading after write: %v", err)
	}
	want := "Start line.\nWe receive data and `seperate` it, but color is real.\n"
	if string(after) != want {
		t.Errorf("file content = %q, want %q", string(after), want)
	}
}

func TestSpellingCheckDirAndExclude(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d1 := filepath.Join(dir, "docs")
	d2 := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(d1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d2, 0o755); err != nil {
		t.Fatal(err)
	}

	f1 := filepath.Join(d1, "guide.md")
	f2 := filepath.Join(d2, "dep.md")
	if err := os.WriteFile(f1, []byte("We recieve this.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("We recieve that.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := check.CheckSpellingDir(dir, check.SpellingOptions{
		Exclude: []string{"vendor"},
	})
	if err != nil {
		t.Fatalf("CheckSpellingDir: %v", err)
	}
	if res.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1", res.FilesScanned)
	}
	if res.Excluded != 1 {
		t.Errorf("Excluded = %d, want 1", res.Excluded)
	}
	if len(res.Findings) != 1 {
		t.Errorf("Findings count = %d, want 1", len(res.Findings))
	}
}

func TestSpellingCheckPatterns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.md")
	f2 := filepath.Join(dir, "b.md")
	if err := os.WriteFile(f1, []byte("Clean content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("We recieve things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pattern := filepath.Join(dir, "*.md")
	res, err := check.CheckSpelling([]string{pattern}, check.SpellingOptions{})
	if err != nil {
		t.Fatalf("CheckSpelling: %v", err)
	}
	if res.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2", res.FilesScanned)
	}
	if len(res.Findings) != 1 {
		t.Errorf("Findings = %d, want 1", len(res.Findings))
	}
}

func TestSpellingMultilineCodeSpan(t *testing.T) {
	t.Parallel()
	text := "Code `recieve\nseperate` remains code.\n"
	findings, updated, err := check.CheckSpellingText("code.md", text, check.SpellingOptions{Markdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for multiline code span, got %+v", findings)
	}
	if updated != text {
		t.Errorf("updated = %q, want %q", updated, text)
	}
}

func TestSpellingContainerFences(t *testing.T) {
	t.Parallel()
	text := "> ```go\n> func recieve() {}\n> ```\n> ~~~python\n> recieve = 1\n> ~~~\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{Markdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Original != "colour" {
		t.Errorf("expected colour, got %+v", findings[0])
	}
}

func TestSpellingEscapedBackticks(t *testing.T) {
	t.Parallel()
	// An odd number of backslashes escapes the backtick, so recieve is prose.
	escaped := "A literal \\`recieve\\` here.\n"
	findings, _, err := check.CheckSpellingText("test.md", escaped, check.SpellingOptions{Markdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Original != "recieve" {
		t.Errorf("expected recieve to be flagged when backtick is escaped, got %+v", findings)
	}

	// An even number of backslashes escapes the backslash, so backtick starts a code span.
	unescaped := "A literal \\\\`recieve\\\\` here.\n"
	findings2, _, err := check.CheckSpellingText("test.md", unescaped, check.SpellingOptions{Markdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings2) != 0 {
		t.Errorf("expected 0 findings when backtick is not escaped, got %+v", findings2)
	}
}

func TestSpellingBlankLineTerminatesSpan(t *testing.T) {
	t.Parallel()
	// A code span cannot cross a blank line (CommonMark 0.31.2).
	text := "`start\n\nrecieve`\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{Markdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Original != "recieve" {
		t.Errorf("expected recieve to be flagged across blank line, got %+v", findings)
	}
}

func TestSpellingSymlinkWriteRefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(root, "outside.md")
	before := "recieve\n"
	if err := os.WriteFile(outside, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	scan := filepath.Join(root, "scan")
	if err := os.Mkdir(scan, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(scan, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	opts := check.SpellingOptions{Write: true}

	// 1. CheckSpellingFile refuses symlink
	_, err := check.CheckSpellingFile(link, opts)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("CheckSpellingFile did not refuse symlink: %v", err)
	}

	// 2. CheckSpellingDir refuses symlink
	_, err = check.CheckSpellingDir(scan, opts)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("CheckSpellingDir did not refuse symlink: %v", err)
	}

	// 3. CheckSpellingFiles refuses symlink
	_, err = check.CheckSpellingFiles(scan, []string{"link.md"}, opts)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("CheckSpellingFiles did not refuse symlink: %v", err)
	}

	// Verify outside target bytes remain strictly unchanged across all attempts
	after, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("outside.md was modified through symlink: got %q, want %q", string(after), before)
	}
}

func TestSpellingRootRelativeExcludeAllModes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	vendorDir := filepath.Join(root, "vendor")
	if err := os.Mkdir(vendorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vendorDir, "doc.md")
	before := "recieve\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := check.SpellingOptions{
		Exclude: []string{"vendor"},
		Write:   true,
		Dir:     root,
	}

	// 1. CheckSpellingDir
	resDir, err := check.CheckSpellingDir(root, opts)
	if err != nil {
		t.Fatalf("CheckSpellingDir: %v", err)
	}
	if resDir.Excluded != 1 || resDir.FilesScanned != 0 {
		t.Errorf("CheckSpellingDir Excluded = %d, FilesScanned = %d", resDir.Excluded, resDir.FilesScanned)
	}

	// 2. CheckSpellingFiles with relative path
	resFiles, err := check.CheckSpellingFiles(root, []string{"vendor/doc.md"}, opts)
	if err != nil {
		t.Fatalf("CheckSpellingFiles: %v", err)
	}
	if resFiles.Excluded != 1 || resFiles.FilesScanned != 0 {
		t.Errorf("CheckSpellingFiles Excluded = %d, FilesScanned = %d", resFiles.Excluded, resFiles.FilesScanned)
	}

	// 3. CheckSpelling with glob pattern
	resGlob, err := check.CheckSpelling([]string{filepath.Join(root, "vendor", "*.md")}, opts)
	if err != nil {
		t.Fatalf("CheckSpelling: %v", err)
	}
	if resGlob.Excluded != 1 || resGlob.FilesScanned != 0 {
		t.Errorf("CheckSpelling glob Excluded = %d, FilesScanned = %d", resGlob.Excluded, resGlob.FilesScanned)
	}

	// Target bytes remain unchanged
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("excluded file was rewritten: got %q, want %q", string(after), before)
	}
}

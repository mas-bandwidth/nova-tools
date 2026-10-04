package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/check"
)

func TestSpellingSeededMisspellings(t *testing.T) {
	t.Parallel()
	text := "I recieve mail.\nWe seperate the parts.\nIt occured to me.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	require.Len(t, findings, 3, "got %d findings, want 3: %+v", len(findings), findings)

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
		assert.Equal(t, exp.line, f.Line, "[%d] line = %d, want %d", i, f.Line, exp.line)
		assert.Equal(t, exp.orig, f.Original, "[%d] orig = %q, want %q", i, f.Original, exp.orig)
		assert.Equal(t, exp.repl, f.Replacement, "[%d] repl = %q, want %q", i, f.Replacement, exp.repl)
	}
}

func TestSpellingBritishVariantsToUS(t *testing.T) {
	t.Parallel()
	text := "The colour of the code.\nWe optimise the loop.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	require.Len(t, findings, 2, "got %d findings, want 2: %+v", len(findings), findings)
	assert.Equal(t, "colour", findings[0].Original, "finding 0 mismatch: %+v", findings[0])
	assert.Equal(t, "color", findings[0].Replacement, "finding 0 mismatch: %+v", findings[0])
	assert.Equal(t, "optimise", findings[1].Original, "finding 1 mismatch: %+v", findings[1])
	assert.Equal(t, "optimize", findings[1].Replacement, "finding 1 mismatch: %+v", findings[1])
}

func TestSpellingAllowlist(t *testing.T) {
	t.Parallel()
	text := "The colour of the cairn.\n"
	opts := check.SpellingOptions{Ignore: []string{"colour"}}
	findings, _, err := check.CheckSpellingText("test.md", text, opts)
	require.NoError(t, err, "unexpected error: %v", err)
	assert.Empty(t, findings, "expected colour to be ignored, got: %+v", findings)
}

func TestSpellingAllowlistCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"colour", "Colour", "COLOUR"} {
		text := "The " + word + " of the cairn.\n"
		opts := check.SpellingOptions{Ignore: []string{"Colour"}}
		findings, _, err := check.CheckSpellingText("test.md", text, opts)
		require.NoError(t, err, "unexpected error: %v", err)
		assert.Empty(t, findings, "expected %q to be ignored by allowlist entry, got: %+v", word, findings)
	}
}

func TestSpellingAllowlistFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	listFile := filepath.Join(dir, "allow.txt")
	content := "# Words we allow\ncolour\n  optimise  # comment\n\n"
	err := os.WriteFile(listFile, []byte(content), 0o644)
	require.NoError(t, err, "writing allowlist file: %v", err)

	opts := check.SpellingOptions{Ignore: []string{"@" + listFile}}
	text := "We optimise the colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, opts)
	require.NoError(t, err, "unexpected error: %v", err)
	assert.Empty(t, findings, "expected findings to be empty with allowlist file, got: %+v", findings)
}

func TestSpellingFencedCodeBlockBlanking(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n```go\nrecieve := seperate(occured)\n```\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	require.Len(t, findings, 1, "got %d findings, want 1: %+v", len(findings), findings)
	assert.Equal(t, 5, findings[0].Line, "finding line or word mismatch: %+v", findings[0])
	assert.Equal(t, "colour", findings[0].Original, "finding line or word mismatch: %+v", findings[0])
}

func TestSpellingTildeFencedCodeBlock(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n~~~python\nrecieve = 1\n~~~\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	require.Len(t, findings, 1, "got %d findings, want 1: %+v", len(findings), findings)
	assert.Equal(t, 5, findings[0].Line, "finding line or word mismatch: %+v", findings[0])
	assert.Equal(t, "colour", findings[0].Original, "finding line or word mismatch: %+v", findings[0])
}

func TestSpellingInlineCodeSpanBlanking(t *testing.T) {
	t.Parallel()
	text := "The `recieve` identifier is code, but occured is prose.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	require.Len(t, findings, 1, "got %d findings, want 1: %+v", len(findings), findings)
	assert.Equal(t, "occured", findings[0].Original, "expected occured, got %+v", findings[0])
}

func TestSpellingUnmatchedBacktickLiteral(t *testing.T) {
	t.Parallel()
	text := "A stray ` and then recieve.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	require.Len(t, findings, 1, "got %d findings, want 1: %+v", len(findings), findings)
	assert.Equal(t, "recieve", findings[0].Original, "expected recieve, got %+v", findings[0])
}

func TestSpellingUnclosedFenceBlanksToEnd(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n```go\nrecieve := 1\nseperate := 2\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err, "unexpected error: %v", err)
	assert.Empty(t, findings, "expected unclosed fence to blank to end, got: %+v", findings)
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
		assert.Equal(t, len(c), len(stripped), "len mismatch: orig %d, stripped %d for %q", len(c), len(stripped), c)
		assert.Equal(t, strings.Count(c, "\n"), strings.Count(stripped, "\n"), "newline count mismatch for %q", c)
	}
}

func TestSpellingCheckDirAndExclude(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d1 := filepath.Join(dir, "docs")
	d2 := filepath.Join(dir, "vendor")
	err := os.MkdirAll(d1, 0o755)
	require.NoError(t, err)
	err = os.MkdirAll(d2, 0o755)
	require.NoError(t, err)

	f1 := filepath.Join(d1, "guide.md")
	f2 := filepath.Join(d2, "dep.md")
	err = os.WriteFile(f1, []byte("We recieve this.\n"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(f2, []byte("We recieve that.\n"), 0o644)
	require.NoError(t, err)

	res, err := check.CheckSpellingDir(dir, check.SpellingOptions{
		Exclude: []string{"vendor"},
	})
	require.NoError(t, err, "CheckSpellingDir: %v", err)
	assert.Equal(t, 1, res.FilesScanned, "FilesScanned = %d, want 1", res.FilesScanned)
	assert.Equal(t, 1, res.Excluded, "Excluded = %d, want 1", res.Excluded)
	assert.Len(t, res.Findings, 1, "Findings count = %d, want 1", len(res.Findings))
}

func TestSpellingCheckPatterns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.md")
	f2 := filepath.Join(dir, "b.md")
	err := os.WriteFile(f1, []byte("Clean content.\n"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(f2, []byte("We recieve things.\n"), 0o644)
	require.NoError(t, err)

	pattern := filepath.Join(dir, "*.md")
	res, err := check.CheckSpelling([]string{pattern}, check.SpellingOptions{})
	require.NoError(t, err, "CheckSpelling: %v", err)
	assert.Equal(t, 2, res.FilesScanned, "FilesScanned = %d, want 2", res.FilesScanned)
	assert.Len(t, res.Findings, 1, "Findings = %d, want 1", len(res.Findings))
}

func TestSpellingMultilineCodeSpan(t *testing.T) {
	t.Parallel()
	text := "Code `recieve\nseperate` remains code.\n"
	findings, updated, err := check.CheckSpellingText("code.md", text, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	assert.Empty(t, findings, "expected 0 findings for multiline code span, got %+v", findings)
	assert.Equal(t, text, updated, "updated = %q, want %q", updated, text)
}

func TestSpellingContainerFences(t *testing.T) {
	t.Parallel()
	text := "> ```go\n> func recieve() {}\n> ```\n> ~~~python\n> recieve = 1\n> ~~~\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	require.Len(t, findings, 1, "got %d findings, want 1: %+v", len(findings), findings)
	assert.Equal(t, "colour", findings[0].Original, "expected colour, got %+v", findings[0])
}

func TestSpellingEscapedBackticks(t *testing.T) {
	t.Parallel()
	// An odd number of backslashes escapes the backtick, so recieve is prose.
	escaped := "A literal \\`recieve\\` here.\n"
	findings, _, err := check.CheckSpellingText("test.md", escaped, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	if len(findings) != 1 || findings[0].Original != "recieve" {
		assert.Failf(t, "assertion failed", "expected recieve to be flagged when backtick is escaped, got %+v", findings)
	}

	// An even number of backslashes escapes the backslash, so backtick starts a code span.
	unescaped := "A literal \\\\`recieve\\\\` here.\n"
	findings2, _, err := check.CheckSpellingText("test.md", unescaped, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	assert.Empty(t, findings2, "expected 0 findings when backtick is not escaped, got %+v", findings2)
}

func TestSpellingBlankLineTerminatesSpan(t *testing.T) {
	t.Parallel()
	// A code span cannot cross a blank line (CommonMark 0.31.2).
	text := "`start\n\nrecieve`\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	if len(findings) != 1 || findings[0].Original != "recieve" {
		assert.Failf(t, "assertion failed", "expected recieve to be flagged across blank line, got %+v", findings)
	}
}

func TestSpellingSymlinkWriteRefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(root, "outside.md")
	before := "recieve\n"
	err := os.WriteFile(outside, []byte(before), 0o600)
	require.NoError(t, err)

	scan := filepath.Join(root, "scan")
	err = os.Mkdir(scan, 0o700)
	require.NoError(t, err)
	link := filepath.Join(scan, "link.md")
	err = os.Symlink(outside, link)
	require.NoError(t, err)

	opts := check.SpellingOptions{Write: true}

	// 1. CheckSpellingDir refuses symlink
	_, err = check.CheckSpellingDir(scan, opts)
	require.ErrorContains(t, err, "is a symlink", "CheckSpellingDir did not refuse symlink: %v", err)

	// 2. CheckSpellingFiles refuses symlink
	_, err = check.CheckSpellingFiles(scan, []string{"link.md"}, opts)
	require.ErrorContains(t, err, "is a symlink", "CheckSpellingFiles did not refuse symlink: %v", err)

	// Verify outside target bytes remain strictly unchanged across all attempts
	after, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, before, string(after), "outside.md was modified through symlink: got %q, want %q", string(after), before)
}

func TestSpellingRootRelativeExcludeAllModes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	vendorDir := filepath.Join(root, "vendor")
	err := os.Mkdir(vendorDir, 0o700)
	require.NoError(t, err)
	path := filepath.Join(vendorDir, "doc.md")
	before := "recieve\n"
	err = os.WriteFile(path, []byte(before), 0o600)
	require.NoError(t, err)

	opts := check.SpellingOptions{
		Exclude: []string{"vendor"},
		Write:   true,
		Dir:     root,
	}

	// 1. CheckSpellingDir
	resDir, err := check.CheckSpellingDir(root, opts)
	require.NoError(t, err, "CheckSpellingDir: %v", err)
	assert.Equal(t, 1, resDir.Excluded, "CheckSpellingDir Excluded = %d, FilesScanned = %d", resDir.Excluded, resDir.FilesScanned)
	assert.Equal(t, 0, resDir.FilesScanned, "CheckSpellingDir Excluded = %d, FilesScanned = %d", resDir.Excluded, resDir.FilesScanned)

	// 2. CheckSpellingFiles with relative path
	resFiles, err := check.CheckSpellingFiles(root, []string{"vendor/doc.md"}, opts)
	require.NoError(t, err, "CheckSpellingFiles: %v", err)
	assert.Equal(t, 1, resFiles.Excluded, "CheckSpellingFiles Excluded = %d, FilesScanned = %d", resFiles.Excluded, resFiles.FilesScanned)
	assert.Equal(t, 0, resFiles.FilesScanned, "CheckSpellingFiles Excluded = %d, FilesScanned = %d", resFiles.Excluded, resFiles.FilesScanned)

	// 3. CheckSpelling with glob pattern
	resGlob, err := check.CheckSpelling([]string{filepath.Join(root, "vendor", "*.md")}, opts)
	require.NoError(t, err, "CheckSpelling: %v", err)
	assert.Equal(t, 1, resGlob.Excluded, "CheckSpelling glob Excluded = %d, FilesScanned = %d", resGlob.Excluded, resGlob.FilesScanned)
	assert.Equal(t, 0, resGlob.FilesScanned, "CheckSpelling glob Excluded = %d, FilesScanned = %d", resGlob.Excluded, resGlob.FilesScanned)

	// Target bytes remain unchanged
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, string(after), "excluded file was rewritten: got %q, want %q", string(after), before)
}

func TestSpellingCommonMarkContainerBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("list-item-code-fence", func(t *testing.T) {
		t.Parallel()
		text := "- ```go\n  func recieve() {}\n  ```\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		assert.Empty(t, findings, "expected 0 findings in list fence, got: %+v", findings)
		assert.Equal(t, text, updated, "updated = %q, want %q", updated, text)
	})

	t.Run("blockquote-ends-fence", func(t *testing.T) {
		t.Parallel()
		text := "> ```go\n> recieve\n\nrecieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		require.Len(t, findings, 1, "expected 1 finding for prose after blockquote fence, got: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 4, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "> ```go\n> recieve\n\nreceive in prose.\n"
		assert.Equal(t, want, updated, "updated = %q, want %q", updated, want)
	})

	t.Run("blank-line-inside-list-fence", func(t *testing.T) {
		t.Parallel()
		text := "- ```go\n  func recieve() {}\n\n  var seperate = 1\n  ```\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		assert.Empty(t, findings, "expected 0 findings in list fence with blank line, got: %+v", findings)
		assert.Equal(t, text, updated, "updated = %q, want %q", updated, text)
	})

	t.Run("loose-list-blank-line-before-fence", func(t *testing.T) {
		t.Parallel()
		text := "-   intro\n\n    ~~~go\n    func recieve() {}\n    ~~~\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		assert.Empty(t, findings, "expected 0 findings in loose list fence, got: %+v", findings)
		assert.Equal(t, text, updated, "updated = %q, want %q", updated, text)
	})

	t.Run("loose-list-unclosed-fence-ends-at-list-exit", func(t *testing.T) {
		t.Parallel()
		text := "- intro\n\n  ~~~go\n  recieve\n\nrecieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		require.Len(t, findings, 1, "expected 1 finding for prose after loose list fence, got: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 6, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "- intro\n\n  ~~~go\n  recieve\n\nreceive in prose.\n"
		assert.Equal(t, want, updated, "updated = %q, want %q", updated, want)
	})

	t.Run("tab-stop-marker-indentation", func(t *testing.T) {
		t.Parallel()
		text := "-\t~~~go\n  recieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		require.Len(t, findings, 1, "expected 1 finding for prose outside tab-indented list item, got: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 2, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "-\t~~~go\n  receive in prose.\n"
		assert.Equal(t, want, updated, "updated = %q, want %q", updated, want)
	})

	t.Run("quoted-list-tab-fence", func(t *testing.T) {
		t.Parallel()
		for _, prefix := range []string{"> - ", "> -\t"} {
			text := prefix + "~~~go\n>   func recieve() {}\n>   ~~~\n"
			findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
			require.NoError(t, err, "CheckSpellingText error: %v", err)
			assert.Empty(t, findings, "prefix %q: expected 0 findings in quoted list fence, got: %+v", prefix, findings)
			assert.Equal(t, text, updated, "prefix %q: updated = %q, want %q", prefix, updated, text)
		}
	})

	t.Run("quoted-list-varying-indent-preserve-code", func(t *testing.T) {
		t.Parallel()
		text := "   > - ~~~go\n>   func recieve() {}\n>   ~~~\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		assert.Empty(t, findings, "expected 0 findings in quoted list fence with shorter quote prefix, got: %+v", findings)
		assert.Equal(t, text, updated, "updated = %q, want %q", updated, text)
	})

	t.Run("quoted-list-varying-indent-correct-prose", func(t *testing.T) {
		t.Parallel()
		text := "> - ~~~go\n   > recieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText error: %v", err)
		require.Len(t, findings, 1, "expected 1 finding for prose outside list item with longer quote prefix, got: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 2, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "> - ~~~go\n   > receive in prose.\n"
		assert.Equal(t, want, updated, "updated = %q, want %q", updated, want)
	})
}

func TestSpellingRelativeDirGlobAndDirectoryExclusion(t *testing.T) {
	t.Parallel()

	t.Run("relative-dir-glob", func(t *testing.T) {
		t.Parallel()
		cwd, err := os.Getwd()
		require.NoError(t, err)
		tmp := t.TempDir()
		docsDir := filepath.Join(tmp, "docs")
		err = os.Mkdir(docsDir, 0o700)
		require.NoError(t, err)
		target := filepath.Join(docsDir, "note.md")
		err = os.WriteFile(target, []byte("recieve\n"), 0o600)
		require.NoError(t, err)
		relDocs, err := filepath.Rel(cwd, docsDir)
		require.NoError(t, err)

		res, err := check.CheckSpelling([]string{filepath.Join(relDocs, "*.md")}, check.SpellingOptions{
			Dir:   relDocs,
			Write: true,
		})
		require.NoError(t, err, "CheckSpelling: %v", err)
		assert.Equal(t, 1, res.FilesScanned, "res = %+v", res)
		assert.Equal(t, 1, res.Corrected, "res = %+v", res)
		after, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.Equal(t, "receive\n", string(after), "after = %q, want %q", string(after), "receive\n")
	})

	t.Run("directory-exclusion-with-target", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		vendor := filepath.Join(root, "vendor")
		err := os.Mkdir(vendor, 0o700)
		require.NoError(t, err)
		path := filepath.Join(vendor, "note.md")
		before := "recieve\n"
		err = os.WriteFile(path, []byte(before), 0o600)
		require.NoError(t, err)

		res, err := check.CheckSpelling([]string{vendor}, check.SpellingOptions{
			Dir:     root,
			Exclude: []string{"vendor"},
			Write:   true,
		})
		require.NoError(t, err, "CheckSpelling: %v", err)
		assert.Equal(t, 1, res.Excluded, "res = %+v, want Excluded=1, FilesScanned=0", res)
		assert.Equal(t, 0, res.FilesScanned, "res = %+v, want Excluded=1, FilesScanned=0", res)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, before, string(after), "excluded directory target rewritten: got %q, want %q", string(after), before)
	})
}

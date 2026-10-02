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
	require.NoError(t, err)
	require.Len(t, findings, 3, "%+v", findings)

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
		assert.Equal(t, exp.line, f.Line, "[%d] line", i)
		assert.Equal(t, exp.orig, f.Original, "[%d] orig", i)
		assert.Equal(t, exp.repl, f.Replacement, "[%d] repl", i)
	}
}

func TestSpellingBritishVariantsToUS(t *testing.T) {
	t.Parallel()
	text := "The colour of the code.\nWe optimise the loop.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err)
	require.Len(t, findings, 2, "%+v", findings)
	assert.Equal(t, "colour", findings[0].Original, "finding 0: %+v", findings[0])
	assert.Equal(t, "color", findings[0].Replacement, "finding 0: %+v", findings[0])
	assert.Equal(t, "optimise", findings[1].Original, "finding 1: %+v", findings[1])
	assert.Equal(t, "optimize", findings[1].Replacement, "finding 1: %+v", findings[1])
}

func TestSpellingAllowlist(t *testing.T) {
	t.Parallel()
	text := "The colour of the cairn.\n"
	opts := check.SpellingOptions{Ignore: []string{"colour"}}
	findings, _, err := check.CheckSpellingText("test.md", text, opts)
	require.NoError(t, err)
	assert.Empty(t, findings, "expected colour to be ignored")
}

func TestSpellingAllowlistCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"colour", "Colour", "COLOUR"} {
		text := "The " + word + " of the cairn.\n"
		opts := check.SpellingOptions{Ignore: []string{"Colour"}}
		findings, _, err := check.CheckSpellingText("test.md", text, opts)
		require.NoError(t, err)
		assert.Empty(t, findings, "expected %q to be ignored by allowlist entry", word)
	}
}

func TestSpellingAllowlistFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	listFile := filepath.Join(dir, "allow.txt")
	content := "# Words we allow\ncolour\n  optimise  # comment\n\n"
	require.NoError(t, os.WriteFile(listFile, []byte(content), 0o644), "writing allowlist file")

	opts := check.SpellingOptions{Ignore: []string{"@" + listFile}}
	text := "We optimise the colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, opts)
	require.NoError(t, err)
	assert.Empty(t, findings, "expected findings to be empty with allowlist file")
}

func TestSpellingFencedCodeBlockBlanking(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n```go\nrecieve := seperate(occured)\n```\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err)
	require.Len(t, findings, 1, "%+v", findings)
	assert.Equal(t, 5, findings[0].Line, "finding line: %+v", findings[0])
	assert.Equal(t, "colour", findings[0].Original, "finding word: %+v", findings[0])
}

func TestSpellingTildeFencedCodeBlock(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n~~~python\nrecieve = 1\n~~~\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err)
	require.Len(t, findings, 1, "%+v", findings)
	assert.Equal(t, 5, findings[0].Line, "finding line: %+v", findings[0])
	assert.Equal(t, "colour", findings[0].Original, "finding word: %+v", findings[0])
}

func TestSpellingInlineCodeSpanBlanking(t *testing.T) {
	t.Parallel()
	text := "The `recieve` identifier is code, but occured is prose.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err)
	require.Len(t, findings, 1, "%+v", findings)
	assert.Equal(t, "occured", findings[0].Original, "%+v", findings[0])
}

func TestSpellingUnmatchedBacktickLiteral(t *testing.T) {
	t.Parallel()
	text := "A stray ` and then recieve.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err)
	require.Len(t, findings, 1, "%+v", findings)
	assert.Equal(t, "recieve", findings[0].Original, "%+v", findings[0])
}

func TestSpellingUnclosedFenceBlanksToEnd(t *testing.T) {
	t.Parallel()
	text := "Prose before.\n```go\nrecieve := 1\nseperate := 2\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{})
	require.NoError(t, err)
	assert.Empty(t, findings, "expected unclosed fence to blank to end")
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
		assert.Len(t, stripped, len(c), "len mismatch for %q", c)
		assert.Equal(t, strings.Count(c, "\n"), strings.Count(stripped, "\n"), "newline count mismatch for %q", c)
	}
}

func TestSpellingCheckDirAndExclude(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d1 := filepath.Join(dir, "docs")
	d2 := filepath.Join(dir, "vendor")
	require.NoError(t, os.MkdirAll(d1, 0o755))
	require.NoError(t, os.MkdirAll(d2, 0o755))

	f1 := filepath.Join(d1, "guide.md")
	f2 := filepath.Join(d2, "dep.md")
	require.NoError(t, os.WriteFile(f1, []byte("We recieve this.\n"), 0o644))
	require.NoError(t, os.WriteFile(f2, []byte("We recieve that.\n"), 0o644))

	res, err := check.CheckSpellingDir(dir, check.SpellingOptions{
		Exclude: []string{"vendor"},
	})
	require.NoError(t, err, "CheckSpellingDir")
	assert.Equal(t, 1, res.FilesScanned, "FilesScanned")
	assert.Equal(t, 1, res.Excluded, "Excluded")
	assert.Len(t, res.Findings, 1, "Findings count")
}

func TestSpellingCheckPatterns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.md")
	f2 := filepath.Join(dir, "b.md")
	require.NoError(t, os.WriteFile(f1, []byte("Clean content.\n"), 0o644))
	require.NoError(t, os.WriteFile(f2, []byte("We recieve things.\n"), 0o644))

	pattern := filepath.Join(dir, "*.md")
	res, err := check.CheckSpelling([]string{pattern}, check.SpellingOptions{})
	require.NoError(t, err, "CheckSpelling")
	assert.Equal(t, 2, res.FilesScanned, "FilesScanned")
	assert.Len(t, res.Findings, 1, "Findings")
}

func TestSpellingMultilineCodeSpan(t *testing.T) {
	t.Parallel()
	text := "Code `recieve\nseperate` remains code.\n"
	findings, updated, err := check.CheckSpellingText("code.md", text, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	assert.Empty(t, findings, "expected 0 findings for multiline code span")
	assert.Equal(t, text, updated)
}

func TestSpellingContainerFences(t *testing.T) {
	t.Parallel()
	text := "> ```go\n> func recieve() {}\n> ```\n> ~~~python\n> recieve = 1\n> ~~~\nProse after with colour.\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	require.Len(t, findings, 1, "%+v", findings)
	assert.Equal(t, "colour", findings[0].Original, "%+v", findings[0])
}

func TestSpellingEscapedBackticks(t *testing.T) {
	t.Parallel()
	// An odd number of backslashes escapes the backtick, so recieve is prose.
	escaped := "A literal \\`recieve\\` here.\n"
	findings, _, err := check.CheckSpellingText("test.md", escaped, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	if assert.Len(t, findings, 1, "expected recieve to be flagged when backtick is escaped: %+v", findings) {
		assert.Equal(t, "recieve", findings[0].Original, "expected recieve to be flagged when backtick is escaped")
	}

	// An even number of backslashes escapes the backslash, so backtick starts a code span.
	unescaped := "A literal \\\\`recieve\\\\` here.\n"
	findings2, _, err := check.CheckSpellingText("test.md", unescaped, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	assert.Empty(t, findings2, "expected 0 findings when backtick is not escaped")
}

func TestSpellingBlankLineTerminatesSpan(t *testing.T) {
	t.Parallel()
	// A code span cannot cross a blank line (CommonMark 0.31.2).
	text := "`start\n\nrecieve`\n"
	findings, _, err := check.CheckSpellingText("test.md", text, check.SpellingOptions{Markdown: true})
	require.NoError(t, err)
	if assert.Len(t, findings, 1, "expected recieve to be flagged across blank line: %+v", findings) {
		assert.Equal(t, "recieve", findings[0].Original, "expected recieve to be flagged across blank line")
	}
}

func TestSpellingSymlinkWriteRefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(root, "outside.md")
	before := "recieve\n"
	require.NoError(t, os.WriteFile(outside, []byte(before), 0o600))

	scan := filepath.Join(root, "scan")
	require.NoError(t, os.Mkdir(scan, 0o700))
	link := filepath.Join(scan, "link.md")
	require.NoError(t, os.Symlink(outside, link))

	opts := check.SpellingOptions{Write: true}

	// CheckSpellingDir refuses the symlink.
	_, err := check.CheckSpellingDir(scan, opts)
	require.ErrorContains(t, err, "is a symlink", "CheckSpellingDir did not refuse symlink")

	// CheckSpellingFiles refuses the symlink.
	_, err = check.CheckSpellingFiles(scan, []string{"link.md"}, opts)
	require.ErrorContains(t, err, "is a symlink", "CheckSpellingFiles did not refuse symlink")

	// The target outside the scan keeps its exact bytes across both attempts.
	after, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, before, string(after), "outside.md was modified through symlink")
}

func TestSpellingRootRelativeExcludeAllModes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	vendorDir := filepath.Join(root, "vendor")
	require.NoError(t, os.Mkdir(vendorDir, 0o700))
	path := filepath.Join(vendorDir, "doc.md")
	before := "recieve\n"
	require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

	opts := check.SpellingOptions{
		Exclude: []string{"vendor"},
		Write:   true,
		Dir:     root,
	}

	// CheckSpellingDir.
	resDir, err := check.CheckSpellingDir(root, opts)
	require.NoError(t, err, "CheckSpellingDir")
	assert.Equal(t, 1, resDir.Excluded, "CheckSpellingDir Excluded")
	assert.Equal(t, 0, resDir.FilesScanned, "CheckSpellingDir FilesScanned")

	// CheckSpellingFiles with a relative path.
	resFiles, err := check.CheckSpellingFiles(root, []string{"vendor/doc.md"}, opts)
	require.NoError(t, err, "CheckSpellingFiles")
	assert.Equal(t, 1, resFiles.Excluded, "CheckSpellingFiles Excluded")
	assert.Equal(t, 0, resFiles.FilesScanned, "CheckSpellingFiles FilesScanned")

	// CheckSpelling with a glob pattern.
	resGlob, err := check.CheckSpelling([]string{filepath.Join(root, "vendor", "*.md")}, opts)
	require.NoError(t, err, "CheckSpelling")
	assert.Equal(t, 1, resGlob.Excluded, "CheckSpelling glob Excluded")
	assert.Equal(t, 0, resGlob.FilesScanned, "CheckSpelling glob FilesScanned")

	// The excluded file keeps its exact bytes.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, string(after), "excluded file was rewritten")
}

func TestSpellingCommonMarkContainerBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("list-item-code-fence", func(t *testing.T) {
		t.Parallel()
		text := "- ```go\n  func recieve() {}\n  ```\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		assert.Empty(t, findings, "expected 0 findings in list fence")
		assert.Equal(t, text, updated)
	})

	t.Run("blockquote-ends-fence", func(t *testing.T) {
		t.Parallel()
		text := "> ```go\n> recieve\n\nrecieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		require.Len(t, findings, 1, "expected 1 finding for prose after blockquote fence: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 4, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "> ```go\n> recieve\n\nreceive in prose.\n"
		assert.Equal(t, want, updated)
	})

	t.Run("blank-line-inside-list-fence", func(t *testing.T) {
		t.Parallel()
		text := "- ```go\n  func recieve() {}\n\n  var seperate = 1\n  ```\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		assert.Empty(t, findings, "expected 0 findings in list fence with blank line")
		assert.Equal(t, text, updated)
	})

	t.Run("loose-list-blank-line-before-fence", func(t *testing.T) {
		t.Parallel()
		text := "-   intro\n\n    ~~~go\n    func recieve() {}\n    ~~~\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		assert.Empty(t, findings, "expected 0 findings in loose list fence")
		assert.Equal(t, text, updated)
	})

	t.Run("loose-list-unclosed-fence-ends-at-list-exit", func(t *testing.T) {
		t.Parallel()
		text := "- intro\n\n  ~~~go\n  recieve\n\nrecieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		require.Len(t, findings, 1, "expected 1 finding for prose after loose list fence: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 6, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "- intro\n\n  ~~~go\n  recieve\n\nreceive in prose.\n"
		assert.Equal(t, want, updated)
	})

	t.Run("tab-stop-marker-indentation", func(t *testing.T) {
		t.Parallel()
		text := "-\t~~~go\n  recieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		require.Len(t, findings, 1, "expected 1 finding for prose outside tab-indented list item: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 2, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "-\t~~~go\n  receive in prose.\n"
		assert.Equal(t, want, updated)
	})

	t.Run("quoted-list-tab-fence", func(t *testing.T) {
		t.Parallel()
		for _, prefix := range []string{"> - ", "> -\t"} {
			text := prefix + "~~~go\n>   func recieve() {}\n>   ~~~\n"
			findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
			require.NoError(t, err, "CheckSpellingText")
			assert.Empty(t, findings, "prefix %q: expected 0 findings in quoted list fence", prefix)
			assert.Equal(t, text, updated, "prefix %q", prefix)
		}
	})

	t.Run("quoted-list-varying-indent-preserve-code", func(t *testing.T) {
		t.Parallel()
		text := "   > - ~~~go\n>   func recieve() {}\n>   ~~~\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		assert.Empty(t, findings, "expected 0 findings in quoted list fence with shorter quote prefix")
		assert.Equal(t, text, updated)
	})

	t.Run("quoted-list-varying-indent-correct-prose", func(t *testing.T) {
		t.Parallel()
		text := "> - ~~~go\n   > recieve in prose.\n"
		findings, updated, err := check.CheckSpellingText("note.md", text, check.SpellingOptions{Markdown: true})
		require.NoError(t, err, "CheckSpellingText")
		require.Len(t, findings, 1, "expected 1 finding for prose outside list item with longer quote prefix: %+v", findings)
		assert.Equal(t, "recieve", findings[0].Original, "unexpected finding: %+v", findings[0])
		assert.Equal(t, 2, findings[0].Line, "unexpected finding: %+v", findings[0])
		want := "> - ~~~go\n   > receive in prose.\n"
		assert.Equal(t, want, updated)
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
		require.NoError(t, os.Mkdir(docsDir, 0o700))
		target := filepath.Join(docsDir, "note.md")
		require.NoError(t, os.WriteFile(target, []byte("recieve\n"), 0o600))
		relDocs, err := filepath.Rel(cwd, docsDir)
		require.NoError(t, err)

		res, err := check.CheckSpelling([]string{filepath.Join(relDocs, "*.md")}, check.SpellingOptions{
			Dir:   relDocs,
			Write: true,
		})
		require.NoError(t, err, "CheckSpelling")
		assert.Equal(t, 1, res.FilesScanned, "res = %+v", res)
		assert.Equal(t, 1, res.Corrected, "res = %+v", res)
		after, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.Equal(t, "receive\n", string(after))
	})

	t.Run("directory-exclusion-with-target", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		vendor := filepath.Join(root, "vendor")
		require.NoError(t, os.Mkdir(vendor, 0o700))
		path := filepath.Join(vendor, "note.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

		res, err := check.CheckSpelling([]string{vendor}, check.SpellingOptions{
			Dir:     root,
			Exclude: []string{"vendor"},
			Write:   true,
		})
		require.NoError(t, err, "CheckSpelling")
		assert.Equal(t, 1, res.Excluded, "res = %+v", res)
		assert.Equal(t, 0, res.FilesScanned, "res = %+v", res)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, before, string(after), "excluded directory target rewritten")
	})
}

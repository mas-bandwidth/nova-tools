package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.EqualValues(t, 2, code, "exit code = %d, want 2", code)
	assert.Contains(t, stderr, "give at least one of --dir, --file, or --path; refusing to guess", "stderr does not contain refusal: %q", stderr)
}

func TestSpellingCLIRefusesNegativeFailMax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, _, stderr := runSpelling(t, "--dir", dir, "--fail-max", "-1")
	require.EqualValues(t, 2, code, "exit code = %d, want 2", code)
	assert.Contains(t, stderr, "--fail-max must be a line ceiling of zero or more", "stderr = %q", stderr)
}

func TestSpellingCLICleanPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "clean.md")
	require.NoError(t, os.WriteFile(f, []byte("A clean sentence with no spelling errors.\n"), 0o644))

	code, stdout, stderr := runSpelling(t, "--dir", dir)
	require.EqualValues(t, 0, code, "exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, "SPELLING OK", "stdout = %q", stdout)
	assert.Contains(t, stdout, "misspellings=0", "stdout = %q", stdout)
}

func TestSpellingCLIFindingsReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "typo.md")
	content := "We recieve the package.\n"
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	code, stdout, stderr := runSpelling(t, "--file", f)
	require.EqualValues(t, 1, code, "exit = %d, want 1; stdout = %q, stderr = %q", code, stdout, stderr)
	assert.Contains(t, stderr, "SPELLING FAIL", "stderr = %q", stderr)
	assert.Contains(t, stderr, "recieve -> receive", "stderr = %q", stderr)
	// Verify file was NOT modified.
	cur, _ := os.ReadFile(f)
	assert.EqualValues(t, content, string(cur), "file modified in read-only mode: %q", string(cur))
}

func TestSpellingCLICodeFencesAndSpansIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "code.md")
	content := "Prose with `recieve` identifier.\n```go\nfunc recieve() {}\n```\nAll clean.\n"
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	code, stdout, stderr := runSpelling(t, "--file", f)
	require.EqualValues(t, 0, code, "exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, "SPELLING OK", "stdout = %q", stdout)
}

func TestSpellingCLIIgnoreFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(f, []byte("The colour of the code is optimise.\n"), 0o644))

	code, stdout, stderr := runSpelling(t, "--file", f, "--ignore", "colour,optimise")
	require.EqualValues(t, 0, code, "exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, "SPELLING OK", "stdout = %q", stdout)
}

func TestSpellingCLIIgnoreFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(f, []byte("The colour of the code.\n"), 0o644))
	ignoreFile := filepath.Join(dir, "ignore.txt")
	require.NoError(t, os.WriteFile(ignoreFile, []byte("# Words to ignore\ncolour\n"), 0o644))

	code, stdout, stderr := runSpelling(t, "--file", f, "--ignore", "@"+ignoreFile)
	require.EqualValues(t, 0, code, "exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, "SPELLING OK", "stdout = %q", stdout)
}

func TestSpellingCLIWriteMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	orig := "We recieve this.\n"
	require.NoError(t, os.WriteFile(f, []byte(orig), 0o644))

	code, stdout, stderr := runSpelling(t, "--file", f, "--write")
	require.EqualValues(t, 0, code, "exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, "SPELLING FIXED", "stdout = %q", stdout)
	assert.Contains(t, stdout, "written=1", "stdout = %q", stdout)

	updated, err := os.ReadFile(f)
	require.NoError(t, err)
	assert.EqualValues(t, "We receive this.\n", string(updated), "updated content = %q, want %q", string(updated), "We receive this.\n")
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
	require.EqualValues(t, 1, code, "exit = %d, want 1; stderr = %q", code, stderr)
	assert.Contains(t, stderr, "docs/a.md", "expected docs/a.md in stderr, got: %q", stderr)
	assert.NotContains(t, stderr, "vendor/b.md", "vendor/b.md should have been excluded, but found in: %q", stderr)
}

func TestSpellingCLIFailMax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "many.md")
	content := "recieve\nseperate\noccured\ncolour\noptimise\n"
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	code, _, stderr := runSpelling(t, "--file", f, "--fail-max", "2")
	require.EqualValues(t, 1, code, "exit = %d, want 1", code)
	assert.Contains(t, stderr, "SPELLING MORE kind=misspelling", "expected MORE line in stderr, got: %q", stderr)
	assert.Contains(t, stderr, "shown=2", "expected shown=2 in summary line, got: %q", stderr)
}

func TestSpellingCLISymlinkWriteRefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scan := filepath.Join(root, "scan")
	require.NoError(t, os.Mkdir(scan, 0o700))
	outside := filepath.Join(root, "outside.md")
	before := "recieve\n"
	require.NoError(t, os.WriteFile(outside, []byte(before), 0o600))
	link := filepath.Join(scan, "link.md")
	require.NoError(t, os.Symlink(outside, link))

	code, stdout, stderr := runSpelling(t, "--dir", scan, "--write")
	require.EqualValues(t, 2, code, "exit = %d, want 2; stdout = %q, stderr = %q", code, stdout, stderr)
	assert.Contains(t, stderr, "is a symlink", "stderr does not report symlink refusal: %q", stderr)

	// Verify outside target bytes remain strictly unchanged.
	after, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.EqualValues(t, before, string(after), "outside file was rewritten through symlink: got %q, want %q", string(after), before)
}

// Directory symlink scan/linkdir -> outside. A lexical path under scan is not
// containment: --file and a glob must both leave outside/note.md unchanged
// and must not exit 0. Same refusal shape as a final-component symlink.
func TestSpellingCLIDirectorySymlinkWriteRefusal(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"file", "glob"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			scan := filepath.Join(root, "scan")
			outside := filepath.Join(root, "outside")
			require.NoError(t, os.Mkdir(scan, 0o700))
			require.NoError(t, os.Mkdir(outside, 0o700))
			note := filepath.Join(outside, "note.md")
			before := "recieve\n"
			require.NoError(t, os.WriteFile(note, []byte(before), 0o600))
			require.NoError(t, os.Symlink(outside, filepath.Join(scan, "linkdir")))

			var code int
			var stdout, stderr string
			switch mode {
			case "file":
				code, stdout, stderr = runSpelling(t, "--dir", scan, "--file", "linkdir/note.md", "--write")
			case "glob":
				code, stdout, stderr = runSpelling(t, "--dir", scan, "--path", "linkdir/*.md", "--write")
			default:
				require.FailNow(t, "fatal prerequisite", "unknown mode %q", mode)
			}
			require.NotEqualValues(t, 0, code, "exit = 0, want non-zero; stdout = %q, stderr = %q", stdout, stderr)
			assert.Contains(t, stderr, "is a symlink", "stderr does not report symlink refusal: %q", stderr)
			after, err := os.ReadFile(note)
			require.NoError(t, err)
			assert.EqualValues(t, before, string(after), "outside/note.md rewritten through directory symlink: got %q, want %q", string(after), before)
		})
	}
}

func TestSpellingCLIRootRelativeExcludeAllModes(t *testing.T) {
	t.Parallel()

	// Mode 1: --dir root with --path glob
	t.Run("dir-with-path-glob", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, "vendor"), 0o700))
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

		code, stdout, stderr := runSpelling(t, "--dir", root, "--path", "vendor/*.md", "--exclude", "vendor", "--write")
		require.EqualValues(t, 0, code, "exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.EqualValues(t, before, string(after), "excluded vendor file rewritten: got %q, want %q", string(after), before)
	})

	// Mode 2: --dir root with --file explicit
	t.Run("dir-with-file-explicit", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, "vendor"), 0o700))
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

		code, stdout, stderr := runSpelling(t, "--dir", root, "--file", "vendor/doc.md", "--exclude", "vendor", "--write")
		require.EqualValues(t, 0, code, "exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.EqualValues(t, before, string(after), "excluded vendor file rewritten: got %q, want %q", string(after), before)
	})

	// Mode 3: --dir root walk alone
	t.Run("dir-walk-alone", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, "vendor"), 0o700))
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

		code, stdout, stderr := runSpelling(t, "--dir", root, "--exclude", "vendor", "--write")
		require.EqualValues(t, 0, code, "exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.EqualValues(t, before, string(after), "excluded vendor file rewritten: got %q, want %q", string(after), before)
	})
}

func TestSpellingCLIMultilineCodeSpan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "code.md")
	before := "Code `recieve\nseperate` remains code.\n"
	require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

	code, stdout, stderr := runSpelling(t, "--file", path, "--write")
	require.EqualValues(t, 0, code, "exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.EqualValues(t, before, string(after), "multiline inline code span was rewritten: got %q, want %q", string(after), before)
}

func TestSpellingCLIContainerFencesAndEscapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "container.md")
	before := "> ```go\n> func recieve() {}\n> ```\n> ~~~python\n> recieve = 1\n> ~~~\n"
	require.NoError(t, os.WriteFile(path, []byte(before), 0o600))

	code, stdout, stderr := runSpelling(t, "--file", path, "--write")
	require.EqualValues(t, 0, code, "exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.EqualValues(t, before, string(after), "code in container fence was rewritten: got %q, want %q", string(after), before)
}

func TestSpellingReviewWitnesses(t *testing.T) {
	t.Parallel()
	t.Run("symlink-write", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		scan := filepath.Join(root, "scan")
		require.NoError(t, os.Mkdir(scan, 0700))
		outside := filepath.Join(root, "outside.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(outside, []byte(before), 0600))
		require.NoError(t, os.Symlink(outside, filepath.Join(scan, "link.md")))
		code, out, err := runSpelling(t, "--dir", scan, "--write")
		after, e := os.ReadFile(outside)
		require.NoError(t, e)
		t.Logf("exit=%d stdout=%q stderr=%q outside=%q", code, out, err, after)
		assert.EqualValues(t, before, string(after), "write crossed selected tree through symlink despite atomic writer refusal")
	})
	t.Run("multiline-code", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "code.md")
		before := "Code `recieve\nseperate` remains code.\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0600))
		code, out, err := runSpelling(t, "--file", path, "--write")
		after, e := os.ReadFile(path)
		require.NoError(t, e)
		t.Logf("exit=%d stdout=%q stderr=%q after=%q", code, out, err, after)
		assert.EqualValues(t, before, string(after), "valid multiline inline code was rewritten")
	})
	t.Run("excluded-glob", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, "vendor"), 0700))
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0600))
		code, out, err := runSpelling(t, "--dir", root, "--path", "vendor/*.md", "--exclude", "vendor", "--write")
		after, e := os.ReadFile(path)
		require.NoError(t, e)
		t.Logf("exit=%d stdout=%q stderr=%q after=%q", code, out, err, after)
		assert.EqualValues(t, before, string(after), "excluded file was rewritten through --path")
	})
}

func TestSpellingRevisedWitnesses(t *testing.T) {
	t.Parallel()
	t.Run("relative-root-glob", func(t *testing.T) {
		t.Parallel()
		cwd, err := os.Getwd()
		require.NoError(t, err)
		tmp := t.TempDir()
		docsDir := filepath.Join(tmp, "docs")
		require.NoError(t, os.Mkdir(docsDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(docsDir, "note.md"), []byte("recieve\n"), 0600))
		relDocs, err := filepath.Rel(cwd, docsDir)
		require.NoError(t, err)
		code, out, errStr := runSpelling(t, "--dir", relDocs, "--path", "*.md", "--write")
		after, e := os.ReadFile(filepath.Join(docsDir, "note.md"))
		require.NoError(t, e)
		t.Logf("exit=%d out=%q err=%q after=%q", code, out, errStr, after)
		assert.Equal(t, 0, code, "relative root joined twice or valid target not corrected")
		assert.Equal(t, "receive\n", string(after), "relative root joined twice or valid target not corrected")
	})
	t.Run("excluded-directory-target", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		vendor := filepath.Join(root, "vendor")
		require.NoError(t, os.Mkdir(vendor, 0700))
		path := filepath.Join(vendor, "note.md")
		before := "recieve\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0600))
		code, out, err := runSpelling(t, "--dir", root, "--path", "vendor", "--exclude", "vendor", "--write")
		after, e := os.ReadFile(path)
		require.NoError(t, e)
		t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
		assert.EqualValues(t, before, string(after), "directory selection bypasses root-relative exclusion")
	})
	for _, tc := range []struct{ name, before, want string }{
		{"list-fence", "- ```go\n  func recieve() {}\n  ```\n", "- ```go\n  func recieve() {}\n  ```\n"},
		{"blockquote-ends-fence", "> ```go\n> recieve\n\nrecieve in prose.\n", "> ```go\n> recieve\n\nreceive in prose.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "note.md")
			require.NoError(t, os.WriteFile(path, []byte(tc.before), 0600))
			code, out, err := runSpelling(t, "--file", path, "--write")
			after, e := os.ReadFile(path)
			require.NoError(t, e)
			t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
			assert.Equal(t, 0, code, "code/prose boundary changed: want %q", tc.want)
			assert.Equal(t, tc.want, string(after), "code/prose boundary changed: want %q", tc.want)
		})
	}
}

func TestSpellingLooseListContainers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, before, want string }{
		{"blank-before-fence-with-four-space-continuation", "-   intro\n\n    ~~~go\n    func recieve() {}\n    ~~~\n", "-   intro\n\n    ~~~go\n    func recieve() {}\n    ~~~\n"},
		{"blank-before-unclosed-list-fence", "- intro\n\n  ~~~go\n  recieve\n\nrecieve in prose.\n", "- intro\n\n  ~~~go\n  recieve\n\nreceive in prose.\n"},
		{"tab-marker-width", "-\t~~~go\n  recieve in prose.\n", "-\t~~~go\n  receive in prose.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "note.md")
			require.NoError(t, os.WriteFile(p, []byte(tc.before), 0600))
			code, out, err := runSpelling(t, "--file", p, "--write")
			after, e := os.ReadFile(p)
			require.NoError(t, e)
			t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
			assert.Equal(t, 0, code, "CommonMark container boundary mismatch: want %q", tc.want)
			assert.Equal(t, tc.want, string(after), "CommonMark container boundary mismatch: want %q", tc.want)
		})
	}
}

func TestSpellingQuotedListTabFence(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"> - ", "> -\t"} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			before := prefix + "~~~go\n>   func recieve() {}\n>   ~~~\n"
			p := filepath.Join(t.TempDir(), "note.md")
			require.NoError(t, os.WriteFile(p, []byte(before), 0600))
			code, out, err := runSpelling(t, "--file", p, "--write")
			after, e := os.ReadFile(p)
			require.NoError(t, e)
			t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
			assert.Equal(t, 0, code, "quoted list fence rewritten: want %q", before)
			assert.Equal(t, before, string(after), "quoted list fence rewritten: want %q", before)
		})
	}
	t.Run("varying-indent-preserve-code", func(t *testing.T) {
		t.Parallel()
		before := "   > - ~~~go\n>   func recieve() {}\n>   ~~~\n"
		p := filepath.Join(t.TempDir(), "note.md")
		require.NoError(t, os.WriteFile(p, []byte(before), 0600))
		code, out, err := runSpelling(t, "--file", p, "--write")
		after, e := os.ReadFile(p)
		require.NoError(t, e)
		t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
		assert.Equal(t, 0, code, "quoted list fence with shorter quote prefix rewritten: want %q, got %q", before, after)
		assert.Equal(t, before, string(after), "quoted list fence with shorter quote prefix rewritten: want %q, got %q", before, after)
	})

	t.Run("varying-indent-correct-prose", func(t *testing.T) {
		t.Parallel()
		before := "> - ~~~go\n   > recieve in prose.\n"
		want := "> - ~~~go\n   > receive in prose.\n"
		p := filepath.Join(t.TempDir(), "note.md")
		require.NoError(t, os.WriteFile(p, []byte(before), 0600))
		code, out, err := runSpelling(t, "--file", p, "--write")
		after, e := os.ReadFile(p)
		require.NoError(t, e)
		t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
		assert.Equal(t, 0, code, "quoted list prose typo with longer quote prefix not corrected: want %q, got %q", want, after)
		assert.Equal(t, want, string(after), "quoted list prose typo with longer quote prefix not corrected: want %q, got %q", want, after)
	})
}

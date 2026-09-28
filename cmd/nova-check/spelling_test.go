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

func TestSpellingCLISymlinkWriteRefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scan := filepath.Join(root, "scan")
	if err := os.Mkdir(scan, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.md")
	before := "recieve\n"
	if err := os.WriteFile(outside, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(scan, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--dir", scan, "--write")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "is a symlink") {
		t.Errorf("stderr does not report symlink refusal: %q", stderr)
	}

	// Verify outside target bytes remain strictly unchanged.
	after, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("outside file was rewritten through symlink: got %q, want %q", string(after), before)
	}
}

func TestSpellingCLIRootRelativeExcludeAllModes(t *testing.T) {
	t.Parallel()

	// Mode 1: --dir root with --path glob
	t.Run("dir-with-path-glob", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "vendor"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runSpelling(t, "--dir", root, "--path", "vendor/*.md", "--exclude", "vendor", "--write")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != before {
			t.Errorf("excluded vendor file rewritten: got %q, want %q", string(after), before)
		}
	})

	// Mode 2: --dir root with --file explicit
	t.Run("dir-with-file-explicit", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "vendor"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runSpelling(t, "--dir", root, "--file", "vendor/doc.md", "--exclude", "vendor", "--write")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != before {
			t.Errorf("excluded vendor file rewritten: got %q, want %q", string(after), before)
		}
	})

	// Mode 3: --dir root walk alone
	t.Run("dir-walk-alone", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "vendor"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runSpelling(t, "--dir", root, "--exclude", "vendor", "--write")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != before {
			t.Errorf("excluded vendor file rewritten: got %q, want %q", string(after), before)
		}
	})
}

func TestSpellingCLIMultilineCodeSpan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "code.md")
	before := "Code `recieve\nseperate` remains code.\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", path, "--write")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("multiline inline code span was rewritten: got %q, want %q", string(after), before)
	}
}

func TestSpellingCLIContainerFencesAndEscapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "container.md")
	before := "> ```go\n> func recieve() {}\n> ```\n> ~~~python\n> recieve = 1\n> ~~~\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSpelling(t, "--file", path, "--write")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stdout = %q, stderr = %q", code, stdout, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("code in container fence was rewritten: got %q, want %q", string(after), before)
	}
}

func TestSpellingReviewWitnesses(t *testing.T) {
	t.Parallel()
	t.Run("symlink-write", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		scan := filepath.Join(root, "scan")
		if e := os.Mkdir(scan, 0700); e != nil {
			t.Fatal(e)
		}
		outside := filepath.Join(root, "outside.md")
		before := "recieve\n"
		if e := os.WriteFile(outside, []byte(before), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(outside, filepath.Join(scan, "link.md")); e != nil {
			t.Fatal(e)
		}
		code, out, err := runSpelling(t, "--dir", scan, "--write")
		after, e := os.ReadFile(outside)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("exit=%d stdout=%q stderr=%q outside=%q", code, out, err, after)
		if string(after) != before {
			t.Error("write crossed selected tree through symlink despite atomic writer refusal")
		}
	})
	t.Run("multiline-code", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "code.md")
		before := "Code `recieve\nseperate` remains code.\n"
		if e := os.WriteFile(path, []byte(before), 0600); e != nil {
			t.Fatal(e)
		}
		code, out, err := runSpelling(t, "--file", path, "--write")
		after, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("exit=%d stdout=%q stderr=%q after=%q", code, out, err, after)
		if string(after) != before {
			t.Error("valid multiline inline code was rewritten")
		}
	})
	t.Run("excluded-glob", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if e := os.Mkdir(filepath.Join(root, "vendor"), 0700); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, "vendor", "doc.md")
		before := "recieve\n"
		if e := os.WriteFile(path, []byte(before), 0600); e != nil {
			t.Fatal(e)
		}
		code, out, err := runSpelling(t, "--dir", root, "--path", "vendor/*.md", "--exclude", "vendor", "--write")
		after, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("exit=%d stdout=%q stderr=%q after=%q", code, out, err, after)
		if string(after) != before {
			t.Error("excluded file was rewritten through --path")
		}
	})
}

func TestSpellingRevisedWitnesses(t *testing.T) {
	t.Parallel()
	t.Run("relative-root-glob", func(t *testing.T) {
		t.Parallel()
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tmp := t.TempDir()
		docsDir := filepath.Join(tmp, "docs")
		if e := os.Mkdir(docsDir, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(docsDir, "note.md"), []byte("recieve\n"), 0600); e != nil {
			t.Fatal(e)
		}
		relDocs, err := filepath.Rel(cwd, docsDir)
		if err != nil {
			t.Fatal(err)
		}
		code, out, errStr := runSpelling(t, "--dir", relDocs, "--path", "*.md", "--write")
		after, e := os.ReadFile(filepath.Join(docsDir, "note.md"))
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("exit=%d out=%q err=%q after=%q", code, out, errStr, after)
		if code != 0 || string(after) != "receive\n" {
			t.Error("relative root joined twice or valid target not corrected")
		}
	})
	t.Run("excluded-directory-target", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		vendor := filepath.Join(root, "vendor")
		if e := os.Mkdir(vendor, 0700); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(vendor, "note.md")
		before := "recieve\n"
		if e := os.WriteFile(path, []byte(before), 0600); e != nil {
			t.Fatal(e)
		}
		code, out, err := runSpelling(t, "--dir", root, "--path", "vendor", "--exclude", "vendor", "--write")
		after, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
		if string(after) != before {
			t.Error("directory selection bypasses root-relative exclusion")
		}
	})
	for _, tc := range []struct{ name, before, want string }{
		{"list-fence", "- ```go\n  func recieve() {}\n  ```\n", "- ```go\n  func recieve() {}\n  ```\n"},
		{"blockquote-ends-fence", "> ```go\n> recieve\n\nrecieve in prose.\n", "> ```go\n> recieve\n\nreceive in prose.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "note.md")
			if e := os.WriteFile(path, []byte(tc.before), 0600); e != nil {
				t.Fatal(e)
			}
			code, out, err := runSpelling(t, "--file", path, "--write")
			after, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
			if code != 0 || string(after) != tc.want {
				t.Errorf("code/prose boundary changed: want %q", tc.want)
			}
		})
	}
}

func TestReviewSpellingLooseListContainers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, before, want string }{
		{"blank-before-fence-with-four-space-continuation", "-   intro\n\n    ~~~go\n    func recieve() {}\n    ~~~\n", "-   intro\n\n    ~~~go\n    func recieve() {}\n    ~~~\n"},
		{"blank-before-unclosed-list-fence", "- intro\n\n  ~~~go\n  recieve\n\nrecieve in prose.\n", "- intro\n\n  ~~~go\n  recieve\n\nreceive in prose.\n"},
		{"tab-marker-width", "-\t~~~go\n  recieve in prose.\n", "-\t~~~go\n  receive in prose.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "note.md")
			if err := os.WriteFile(p, []byte(tc.before), 0600); err != nil {
				t.Fatal(err)
			}
			code, out, err := runSpelling(t, "--file", p, "--write")
			after, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
			if code != 0 || string(after) != tc.want {
				t.Errorf("CommonMark container boundary mismatch: want %q", tc.want)
			}
		})
	}
}

func TestStellaQuotedListTabFence(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"> - ", "> -\t"} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			before := prefix + "~~~go\n>   func recieve() {}\n>   ~~~\n"
			p := filepath.Join(t.TempDir(), "note.md")
			if err := os.WriteFile(p, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			code, out, err := runSpelling(t, "--file", p, "--write")
			after, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("exit=%d out=%q err=%q after=%q", code, out, err, after)
			if code != 0 || string(after) != before {
				t.Errorf("quoted list fence rewritten: want %q", before)
			}
		})
	}
}
